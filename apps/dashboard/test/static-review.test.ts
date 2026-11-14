import { describe, expect, it } from "vitest";
import { prepareStaticSnapshot, verifiedRasterDataURL, STATIC_IMAGE_MAX_BYTES, STATIC_IMAGES_TOTAL_MAX_BYTES } from "@/lib/collab/static-renderer";
import { snapshotPagePath, staticAssetPath } from "@/lib/collab/static-snapshot";

const target = { siteId: "site", versionId: "version", contentHash: "a".repeat(64), page: "/index.html" };
const hostile = `<base href="https://evil.test"><meta http-equiv="refresh" content="0;url=https://evil.test"><script>parent.__attacked=1</script><style>@import 'https://evil.test/x';p{background:url(https://evil.test/y)}</style><iframe srcdoc="<script>alert(1)</script>"></iframe><svg><a xlink:href="javascript:alert(1)">svg</a></svg><math><mtext><img src=x onerror=alert(1)></mtext></math><form action="https://evil.test"><input name=x autofocus onfocus=alert(1)><button formaction="https://evil.test">Send</button></form><p id="clobber" name="parent" class="evil" style="background:url(https://evil.test/z)" onclick="parent.__attacked=2" data-component-id="hero">Hello &lt;script&gt;world&lt;/script&gt;</p><a href="javascript:alert(1)" ping="https://evil.test/p">Link text</a><img src="https://evil.test/image" srcset="https://evil.test/b 2x" onerror="alert(1)" alt="external"><object data="https://evil.test/object"></object><template shadowrootmode="open"><img src="https://evil.test/shadow"></template>`;

export { hostile };

describe("trusted static snapshot reconstruction", () => {
  it("removes executable markup, CSS, URL attributes, forms, embeds and foreign namespaces", () => {
    const snapshot = prepareStaticSnapshot(hostile, target.page).render(new Map(), target);
    expect(snapshot.html).toContain("Hello &lt;script&gt;world&lt;/script&gt;");
    expect(snapshot.html).toContain("Link text");
    expect(snapshot.html).toContain('data-review-component="static-v1:hero"');
    expect(snapshot.html).toContain("script-src &#39;none&#39;");
    expect(snapshot.html).not.toMatch(/<(script|iframe|svg|math|form|input|button|object|template|base)\b/i);
    expect(snapshot.html.split("<body>")[1]).not.toMatch(/\s(on[a-z]+|href|srcset|style|name|ping)=/i);
    expect(snapshot.html).not.toContain("https://evil.test");
    expect(snapshot.html).not.toContain("parent.__attacked");
    expect(snapshot.omittedAssets).toBe(1);
  });
  it("fetches only same-version relative raster paths and never uses original data URLs", () => {
    const source = prepareStaticSnapshot('<img src="../assets/photo.png"><img src="data:image/png;base64,AAAA"><img src="https://external.test/x">', "/docs/page.html");
    expect(source.imagePaths).toEqual(["assets/photo.png"]);
    const bytes = Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]), Buffer.alloc(8)]);
    const raster = verifiedRasterDataURL(bytes, "image/png")!;
    const rendered = source.render(new Map([["assets/photo.png", raster]]), target);
    expect(rendered.html).toContain(raster);
    expect(rendered.omittedAssets).toBe(2);
    expect(verifiedRasterDataURL(Buffer.from("<svg onload='alert(1)'></svg>"), "image/svg+xml")).toBeNull();
    expect(verifiedRasterDataURL(Buffer.from("<svg onload='alert(1)'></svg>"), "image/png")).toBeNull();
  });
  it("bounds input and rejects URL, traversal and encoded-backslash asset paths", () => {
    expect(() => prepareStaticSnapshot("x".repeat(512 * 1024 + 1), "/")).toThrow();
    for (const source of ["https://evil.test/x", "//evil.test/x", "javascript:alert(1)", "data:image/png,x", "/x%5cevil", "/x?token=secret"]) expect(staticAssetPath("/index.html", source)).toBeNull();
    expect(snapshotPagePath("/")).toBe("index.html");
    expect(snapshotPagePath("/docs/")).toBe("docs/index.html");
  });
  it("accepts a 2 MiB raster but bounds total embedded bytes, including repeated paths", () => {
    const image = Buffer.alloc(STATIC_IMAGE_MAX_BYTES);
    Buffer.from([137,80,78,71,13,10,26,10]).copy(image);
    const data = verifiedRasterDataURL(image, "image/png")!;
    expect(data).toBeTruthy();
    expect(verifiedRasterDataURL(Buffer.concat([image, Buffer.from([0])]), "image/png")).toBeNull();
    const repeated = prepareStaticSnapshot('<img src="image.png">'.repeat(12), "/index.html").render(new Map([["image.png", data]]), target);
    expect((repeated.html.match(/<img\b/g) ?? []).length).toBe(STATIC_IMAGES_TOTAL_MAX_BYTES / STATIC_IMAGE_MAX_BYTES);
    expect(repeated.omittedAssets).toBe(10);
    expect(repeated.html.length).toBeLessThan(STATIC_IMAGES_TOTAL_MAX_BYTES * 1.4);
  });
});
