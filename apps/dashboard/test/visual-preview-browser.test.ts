import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { chromium } from "@playwright/test";
import { describe, expect, it } from "vitest";
import { chromiumPage } from "./chromium-fixture";
import { renderComparisonPage } from "@/lib/project-room/visual-preview";
import type { ReviewCandidate } from "@/lib/project-room/contract";
const chrome =
  process.env.COLLAB_CHROME_PATH ||
  [chromium.executablePath(), "/opt/google/chrome/chrome"].find(existsSync) ||
  "";
describe.skipIf(!existsSync(chrome))("visual proposal sandbox", () => {
  const getPage = chromiumPage(chrome);
  it("preserves local styling while blocking scripts and outbound requests", async () => {
    const page = getPage(),
      requests: string[] = [];
    await page.route("https://attacker.invalid/**", (route) => {
      requests.push(route.request().url());
      return route.abort();
    });
    const artifact = (path: string, text: string) => ({
      path,
      data: Buffer.from(text).toString("base64"),
      sha256: createHash("sha256").update(text).digest("hex"),
    });
    const candidate = {
      ID: "c",
      BaseContentHash: "base",
      ContentHash: "next",
    } as ReviewCandidate;
    const preview = renderComparisonPage(
      {
        candidate_id: "c",
        base_content_hash: "base",
        content_hash: "next",
        before: [],
        after: [
          artifact(
            "index.html",
            '<link rel="stylesheet" href="style.css"><script>parent.attacked=true</script><img src="https://attacker.invalid/img"><h1 onclick="parent.attacked=true">Proposed title</h1><a href="https://attacker.invalid/next">Link</a>',
          ),
          artifact(
            "style.css",
            "h1 {color:rgb(200, 0, 0);font-size:32px} body{background:url(https://attacker.invalid/bg)}",
          ),
        ],
      },
      candidate,
      "index.html",
    );
    await page.setContent("<main>Trusted dashboard</main>");
    await page.evaluate((html) => {
      const iframe = document.createElement("iframe");
      iframe.setAttribute("sandbox", "");
      iframe.srcdoc = html;
      document.body.append(iframe);
    }, preview.after!);
    const frame = page.frameLocator("iframe");
    await frame.getByRole("heading").waitFor();
    expect(
      await frame
        .getByRole("heading")
        .evaluate((el) => getComputedStyle(el).color),
    ).toBe("rgb(200, 0, 0)");
    await frame.getByRole("heading").click();
    expect(
      await page.evaluate(() => Reflect.get(window, "attacked")),
    ).toBeUndefined();
    expect(requests).toEqual([]);
    expect(await page.locator("iframe").getAttribute("sandbox")).toBe("");
  });
});
