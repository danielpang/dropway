// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Chrome checks for the selection bridge. These observe network and frame
// behavior, not only the final DOM. Skipped when the system Chrome binary
// is absent so the suite does not download a browser.

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { existsSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { armPreviewFrame, createFrameSession, guestBridgeSource, noteFrameLoad, validateBridgeEvent } from "@/lib/collab";

const CHROME = "/opt/google/chrome/chrome";
const hasChrome = existsSync(CHROME);

async function listen(
  handler: (req: IncomingMessage, res: ServerResponse) => void,
): Promise<{ server: Server; origin: string }> {
  const server = createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("no port");
  return { server, origin: `http://127.0.0.1:${address.port}` };
}

describe.skipIf(!hasChrome)("collab browser behavior", () => {
  it("omits cookies only when credentialless is set before src", async () => {
    const hits: { url: string; cookie: string }[] = [];
    const site = await listen((req, res) => {
      hits.push({ url: req.url ?? "", cookie: req.headers.cookie ?? "" });
      if ((req.url ?? "").startsWith("/parent")) {
        res.setHeader("Set-Cookie", "session=secret; Path=/; SameSite=Lax");
      }
      res.setHeader("Content-Type", "text/html");
      res.end("<!doctype html><p>ok</p>");
    });
    const { chromium } = await import("@playwright/test");
    const browser = await chromium.launch({
      executablePath: CHROME,
      headless: true,
      args: ["--no-sandbox"],
    });
    try {
      const page = await browser.newPage();
      await page.goto(`${site.origin}/parent`);
      const late = await page.evaluate(async (origin) => {
        const iframe = document.createElement("iframe");
        iframe.setAttribute("sandbox", "allow-scripts");
        iframe.src = `${origin}/late`;
        document.body.appendChild(iframe);
        iframe.setAttribute("credentialless", "");
        await new Promise((resolve) => (iframe.onload = () => resolve(true)));
        return (iframe as HTMLIFrameElement & { credentialless?: boolean }).credentialless === true;
      }, site.origin);
      const armed = await page.evaluate(async ({ origin, source }) => {
        const arm = new Function(`return (${source})`)() as (
          frame: HTMLIFrameElement,
          src: string,
        ) => { ok: boolean };
        const iframe = document.createElement("iframe");
        const result = arm(iframe, `${origin}/early`);
        document.body.appendChild(iframe);
        await new Promise((resolve) => (iframe.onload = () => resolve(true)));
        return {
          result,
          credentialless: (iframe as HTMLIFrameElement & { credentialless?: boolean }).credentialless === true,
        };
      }, { origin: site.origin, source: armPreviewFrame.toString() });
      const lateHits = hits.filter((hit) => hit.url.startsWith("/late"));
      const early = hits.filter((hit) => hit.url.startsWith("/early"));
      expect(late).toBe(true);
      expect(lateHits[0]?.cookie).toBe("session=secret");
      expect(armed.credentialless).toBe(true);
      expect(armed.result).toEqual({ ok: true });
      expect(early[0]?.cookie).toBe("");
      expect(early.slice(1).every((hit) => hit.cookie === "")).toBe(true);
    } finally {
      await browser.close();
      site.server.close();
    }
  }, 20000);

  it("drops selections posted to a hostile framing parent", async () => {
    const trusted = await listen((_req, res) => {
      res.setHeader("Content-Type", "text/html");
      res.end("<!doctype html><p>dashboard</p>");
    });
    const guest = guestBridgeSource({ BETTER_AUTH_URL: trusted.origin });
    const content = await listen((_req, res) => {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(`<!doctype html><html data-dropway-review="1"><body><p id="q">hello unique</p><script>${guest}</script></body></html>`);
    });
    const hostile = await listen((_req, res) => {
      res.setHeader("Content-Type", "text/html");
      res.end("<!doctype html><p>hostile</p>");
    });
    const { chromium } = await import("@playwright/test");
    const browser = await chromium.launch({
      executablePath: CHROME,
      headless: true,
      args: ["--no-sandbox"],
    });
    try {
      const page = await browser.newPage();
      async function frameFrom(parentOrigin: string) {
        await page.goto(`${parentOrigin}/`);
        await page.evaluate(async (contentOrigin) => {
          (window as unknown as { __msgs: unknown[] }).__msgs = [];
          window.addEventListener("message", (event) => {
            (window as unknown as { __msgs: unknown[] }).__msgs.push(event.data);
          });
          const iframe = document.createElement("iframe");
          iframe.src = `${contentOrigin}/`;
          document.body.appendChild(iframe);
          await new Promise((resolve) => (iframe.onload = () => resolve(true)));
          iframe.contentWindow?.postMessage(
            {
              channel: "dropway.collab.v1",
              type: "ready",
              nonce: "n".repeat(24),
              origin: "https://app.dropway.dev",
            },
            "*",
          );
        }, content.origin);
        const frame = page.frames().find((entry) => entry.url().startsWith(content.origin));
        if (!frame) throw new Error("missing content frame");
        await frame.evaluate(() => {
          const node = document.querySelector("#q");
          if (!node) return;
          const range = document.createRange();
          range.selectNodeContents(node);
          const selection = window.getSelection();
          selection?.removeAllRanges();
          selection?.addRange(range);
          window.dispatchEvent(new MouseEvent("mouseup", { bubbles: true }));
        });
        await page.waitForTimeout(50);
        return page.evaluate(() => (window as unknown as { __msgs: unknown[] }).__msgs);
      }
      const hostileMessages = await frameFrom(hostile.origin);
      expect(JSON.stringify(hostileMessages)).not.toContain("hello unique");
      const trustedMessages = await frameFrom(trusted.origin);
      expect(JSON.stringify(trustedMessages)).toContain("hello unique");
    } finally {
      await browser.close();
      trusted.server.close();
      content.server.close();
      hostile.server.close();
    }
  }, 20000);

  it("keeps a child navigation invisible on iframe.src and rejects its opaque messages", async () => {
    const site = await listen((req, res) => {
      const doc = new URL(req.url ?? "/", "http://127.0.0.1").searchParams.get("doc") ?? "A";
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(`<!doctype html><title>${doc}</title><script>
        parent.postMessage({ doc: ${JSON.stringify(doc)}, href: location.href }, "*");
        if (${JSON.stringify(doc)} === "A") location.replace(location.pathname + "?doc=B");
      </script>`);
    });
    const { chromium } = await import("@playwright/test");
    const browser = await chromium.launch({
      executablePath: CHROME,
      headless: true,
      args: ["--no-sandbox"],
    });
    try {
      const page = await browser.newPage();
      const observed = await page.evaluate(async (origin) => {
        const reports: { data: { doc: string; href: string }; origin: string }[] = [];
        let loads = 0;
        window.addEventListener("message", (event) => {
          if (event.data && event.data.doc) reports.push({ data: event.data, origin: event.origin });
        });
        const iframe = document.createElement("iframe");
        iframe.setAttribute("sandbox", "allow-scripts");
        iframe.addEventListener("load", () => {
          loads += 1;
        });
        iframe.src = `${origin}/?doc=A#dw-bridge=nonceA`;
        document.body.appendChild(iframe);
        await new Promise((resolve) => setTimeout(resolve, 300));
        const srcAfterChild = iframe.src;
        const loadsAfterChild = loads;
        iframe.src = `${origin}/?doc=C`;
        await new Promise((resolve) => (iframe.onload = () => resolve(true)));
        return { srcAfterChild, loadsAfterChild, loads, reports };
      }, site.origin);
      expect(observed.srcAfterChild).toContain("doc=A");
      expect(observed.srcAfterChild).toContain("dw-bridge=nonceA");
      expect(observed.loadsAfterChild).toBe(1);
      let session = createFrameSession("n".repeat(24));
      for (let i = 0; i < observed.loads; i += 1) session = noteFrameLoad(session);
      expect(session.invalidated).toBe(true);
      expect(observed.reports.map((entry) => entry.data.doc)).toEqual(["A", "B", "C"]);
      expect(observed.reports.every((entry) => entry.origin === "null")).toBe(true);
      expect(observed.reports[1]?.data.href).not.toContain("dw-bridge");
      const source = { id: "frame" };
      expect(
        validateBridgeEvent(
          {
            nonce: "n".repeat(24),
            previewOrigin: "https://acme.dropwaycontent.com",
            source,
            parentOrigin: "https://app.dropway.dev",
          },
          {
            origin: "null",
            source,
            data: {
              channel: "dropway.collab.v1",
              nonce: "n".repeat(24),
              type: "hello",
              surface: "instrumented-html",
            },
          },
        ),
      ).toEqual({ ok: false, reason: "opaque-origin" });
    } finally {
      await browser.close();
      site.server.close();
    }
  }, 20000);
});
