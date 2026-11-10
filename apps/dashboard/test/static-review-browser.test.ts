import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { chromium } from "@playwright/test";
import { describe, expect, it } from "vitest";
import { prepareStaticSnapshot } from "@/lib/collab/static-renderer";
import { captureStaticText, captureStaticPoint } from "@/lib/collab/static-selection";

const chrome = process.env.COLLAB_CHROME_PATH || [chromium.executablePath(), "/opt/google/chrome/chrome"].find(existsSync) || "";
describe.skipIf(!existsSync(chrome))("scriptless static review in Chromium", () => {
  it("blocks source scripts/navigation/resources while the trusted parent can select immutable text and points", async () => {
    const hits: string[] = [];
    const server = createServer((req, res) => { hits.push(req.url ?? ""); res.setHeader("Content-Type", "text/html"); res.end("<!doctype html><p>Trusted dashboard</p>"); });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("No port");
    const origin = `http://127.0.0.1:${address.port}`;
    const hostile = `<script>parent.__attacked=true;location='${origin}/navigate'</script><meta http-equiv="refresh" content="0;url=${origin}/refresh"><style>@import '${origin}/css';p{background:url(${origin}/background)}</style><img src="${origin}/image" onerror="parent.__attacked=true"><form action="${origin}/form"><input autofocus onfocus="parent.__attacked=true"></form><p data-component-id="caption" onclick="parent.__attacked=true">Hello immutable world</p><a href="${origin}/navigation" ping="${origin}/ping">Read</a><svg onload="parent.__attacked=true"></svg>`;
    const snapshot = prepareStaticSnapshot(hostile, "/index.html").render(new Map(), { siteId: "site", versionId: "version", contentHash: "a".repeat(64), page: "/index.html" });
    const browser = await chromium.launch({ executablePath: chrome, headless: true, args: ["--no-sandbox"] });
    try {
      const page = await browser.newPage();
      await page.goto(origin);
      const result = await page.evaluate(async ({ html, textSource, pointSource }) => {
        const frame = document.createElement("iframe");
        frame.setAttribute("sandbox", "allow-same-origin");
        frame.referrerPolicy = "no-referrer";
        const loaded = new Promise<void>((resolve) => frame.addEventListener("load", () => resolve(), { once: true }));
        frame.srcdoc = html;
        document.body.appendChild(frame);
        await loaded;
        const doc = frame.contentDocument!;
        const element = doc.querySelector('[data-review-component="static-v1:caption"]')!;
        const range = doc.createRange(); range.selectNodeContents(element);
        doc.getSelection()!.removeAllRanges(); doc.getSelection()!.addRange(range);
        const captureText = new Function(`return (${textSource})`)() as typeof captureStaticText;
        const capturePoint = new Function(`return (${pointSource})`)() as typeof captureStaticPoint;
        const text = captureText(doc);
        const point = capturePoint(doc, { clientX: 20, clientY: 20 } as MouseEvent);
        // A second independent defense: even a script deliberately inserted by trusted parent code is inert.
        const script = doc.createElement("script"); script.textContent = "parent.__attacked=true"; doc.body.appendChild(script);
        (element as HTMLElement).click();
        return { text, point, url: doc.URL, sandbox: frame.sandbox.value, attacked: Boolean((window as unknown as { __attacked?: boolean }).__attacked), forbidden: doc.querySelectorAll("form,iframe,svg,input,a[href],meta[http-equiv=refresh]").length };
      }, { html: snapshot.html, textSource: captureStaticText.toString(), pointSource: captureStaticPoint.toString() });
      expect(result.text).toMatchObject({ kind: "text", anchor: { quote: "Hello immutable world", start_offset: 0, end_offset: 21, block_id: "static-v1:caption" } });
      expect(result.point?.kind).toBe("point");
      expect(result.sandbox).toBe("allow-same-origin");
      expect(result.url).toBe("about:srcdoc");
      expect(result.attacked).toBe(false);
      expect(result.forbidden).toBe(0);
      expect(hits.filter((path) => !["/", "/favicon.ico"].includes(path))).toEqual([]);
    } finally { await browser.close(); server.close(); }
  }, 20000);
});
