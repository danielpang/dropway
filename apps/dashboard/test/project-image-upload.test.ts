import { describe, expect, it } from "vitest";
import { projectImageFiles, PROJECT_IMAGE_MAX_BYTES } from "@/lib/project-room/image-upload";

describe("project image uploads", () => {
  it("stores a verified raster with an inert HTML wrapper and a stable image identity", async () => {
    const bytes = new Uint8Array([137,80,78,71,13,10,26,10,0,0,0,0]);
    const files = await projectImageFiles(new File([bytes], '<script>alert("x")</script>.png', { type:"image/png" }));
    expect(files.map((file) => file.path)).toEqual(["index.html", "image.png"]);
    const html = await files[0]!.file.text();
    expect(html).toContain('data-component-id="project-image"');
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>');
  });
  it("rejects active formats and MIME spoofing", async () => {
    await expect(projectImageFiles(new File(['<svg onload="alert(1)"></svg>'], "fake.png", {type:"image/png"}))).rejects.toThrow("not a supported");
  });
  it("rejects images above the bounded preview size", async () => {
    await expect(projectImageFiles(new File([new Uint8Array(PROJECT_IMAGE_MAX_BYTES+1)], "large.png"))).rejects.toThrow("up to 2 MB");
  });
});
