import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { chromium } from "@playwright/test";
import { describe, expect, it } from "vitest";
import { chromiumPage } from "./chromium-fixture";
import { prepareStaticSnapshot } from "@/lib/collab/static-renderer";
import { captureStaticText, captureStaticPoint, staticAnnotationRects } from "@/lib/collab/static-selection";

const chrome = process.env.COLLAB_CHROME_PATH || [chromium.executablePath(), "/opt/google/chrome/chrome"].find(existsSync) || "";
describe.skipIf(!existsSync(chrome))("scriptless static review in Chromium", () => {
  const getPage = chromiumPage(chrome);
  it("blocks source scripts/navigation/resources while the trusted parent can select immutable text and points", async () => {
    const hits: string[] = [];
    const server = createServer((req, res) => { hits.push(req.url ?? ""); res.setHeader("Content-Type", "text/html"); res.end("<!doctype html><p>Trusted dashboard</p>"); });
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("No port");
    const origin = `http://127.0.0.1:${address.port}`;
    const hostile = `<script>parent.__attacked=true;location='${origin}/navigate'</script><meta http-equiv="refresh" content="0;url=${origin}/refresh"><style>@import '${origin}/css';p{background:url(${origin}/background)}</style><img src="${origin}/image" onerror="parent.__attacked=true"><form action="${origin}/form"><input autofocus onfocus="parent.__attacked=true"></form><p data-component-id="caption" onclick="parent.__attacked=true">Hello immutable world</p><a href="${origin}/navigation" ping="${origin}/ping">Read</a><svg onload="parent.__attacked=true"></svg>`;
    const pixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=";
    const snapshot = prepareStaticSnapshot(hostile + '<img src="pixel.png" data-component-id="photo">', "/index.html").render(new Map([["pixel.png", pixel]]), { siteId: "site", versionId: "version", contentHash: "a".repeat(64), page: "/index.html" });
    try {
      const page = getPage();
      await page.goto(origin);
      const result = await page.evaluate(async ({ html, textSource, pointSource, annotationSource }) => {
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
        const annotationRects = new Function(`return (${annotationSource})`)() as typeof staticAnnotationRects;
        const text = captureText(doc);
        const point = capturePoint(doc, { clientX: 20, clientY: 20 } as MouseEvent);
        const before = doc.getElementById("review-content")!.textContent;
        const textRects = annotationRects(doc, { id: "comment-1", kind: "text", anchor: text!.anchor });
        const componentRects = annotationRects(doc, { id: "comment-2", kind: "component", anchor: { stable_id: "static-v1:caption" } });
        const staleRects = annotationRects(doc, { id: "comment-3", kind: "text", anchor: { ...text!.anchor, quote: "Different version" } });
        const missingRects = annotationRects(doc, { id: "comment-4", kind: "component", anchor: { stable_id: 'caption"] script' } });
        const wrongRenditionRects = annotationRects(doc, { id: "comment-5", kind: "point", anchor: { ...point!.anchor, rendition: { width: 1, height: 1 } } });
        // Parent-installed annotation UI is outside the immutable text scope.
        const overlay = doc.createElement("div"); overlay.textContent = "1 Comment marker"; doc.body.appendChild(overlay);
        overlay.style.cssText = "position:fixed;left:10px;top:10px;width:26px;height:26px;background:rgb(37, 99, 235)";
        const overlayStyle = doc.defaultView!.getComputedStyle(overlay);
        const overlayRendered = overlayStyle.position === "fixed" && overlayStyle.backgroundColor === "rgb(37, 99, 235)" && overlay.getBoundingClientRect().width === 26;
        const after = captureText(doc);
        const photo = doc.querySelector('[data-review-component="static-v1:photo"]') as HTMLImageElement;
        await photo.decode();
        photo.style.width = "160px";
        const photoRect = photo.getBoundingClientRect();
        const imagePoint = capturePoint(doc, { target: photo, clientX: photoRect.right - 0.1, clientY: photoRect.bottom - 0.1 } as unknown as MouseEvent)!;
        photo.style.width = "80px";
        const imageRects = annotationRects(doc, { id: "comment-image", kind: "point", anchor: imagePoint.anchor });
        const legacyImage = { ...(imagePoint.anchor.image as Record<string, unknown>) }; delete legacyImage.stable_id;
        const legacyImageRects = annotationRects(doc, { id: "legacy-image", kind: "point", anchor: { ...imagePoint.anchor, image: legacyImage } });
        const imageMarkerFollowsResize = imageRects.length === 1 && Math.abs(imageRects[0]!.left - photo.getBoundingClientRect().right) < 1;
        // A second independent defense: even a script deliberately inserted by trusted parent code is inert.
        const script = doc.createElement("script"); script.textContent = "parent.__attacked=true"; doc.body.appendChild(script);
        (element as HTMLElement).click();
        return { text, point, imagePoint, overlayRendered, imageMarkerFollowsResize, legacyImageMarkers: legacyImageRects.length, annotations: { text: textRects.length, component: componentRects.length, stale: staleRects.length, missing: missingRects.length, wrongRendition: wrongRenditionRects.length, unchanged: before === doc.getElementById("review-content")!.textContent && JSON.stringify(after) === JSON.stringify(text) }, url: doc.URL, sandbox: frame.sandbox.value, attacked: Boolean((window as unknown as { __attacked?: boolean }).__attacked), forbidden: doc.querySelectorAll("form,iframe,svg,input,a[href],meta[http-equiv=refresh]").length };
      }, { html: snapshot.html, textSource: captureStaticText.toString(), pointSource: captureStaticPoint.toString(), annotationSource: staticAnnotationRects.toString() });
      expect(result.text).toMatchObject({ kind: "text", anchor: { quote: "Hello immutable world", start_offset: 0, end_offset: 21, block_id: "static-v1:caption" } });
      expect(result.point?.kind).toBe("point");
      expect(result.annotations.text).toBeGreaterThan(0);
      expect(result.annotations.component).toBeGreaterThan(0);
      expect(result.annotations).toMatchObject({ stale: 0, missing: 0, wrongRendition: 0, unchanged: true });
      expect(result.overlayRendered).toBe(true);
      expect(result.imagePoint.anchor).toMatchObject({ image: { stable_id: "static-v1:photo" } });
      expect(result.imageMarkerFollowsResize).toBe(true);
      expect(result.legacyImageMarkers).toBe(0);
      expect(result.sandbox).toBe("allow-same-origin");
      expect(result.url).toBe("about:srcdoc");
      expect(result.attacked).toBe(false);
      expect(result.forbidden).toBe(0);
      expect(hits.filter((path) => !["/", "/favicon.ico"].includes(path))).toEqual([]);
    } finally { server.close(); }
  }, 20000);
});
