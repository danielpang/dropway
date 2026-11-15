import type { DroppedFile } from "@/lib/deploy-manifest";

export const PROJECT_IMAGE_MAX_BYTES = 2 * 1024 * 1024;

/** Verify supported raster signatures before allocating a resource. Filenames
 * are display text only; the stored paths are fixed and the wrapper is inert. */
export async function projectImageFiles(file: File): Promise<DroppedFile[]> {
  if (file.size < 12 || file.size > PROJECT_IMAGE_MAX_BYTES) throw new Error("Choose a PNG, JPEG or WebP image up to 2 MB.");
  const bytes = new Uint8Array(await file.slice(0, 12).arrayBuffer());
  let extension: string;
  if ([137, 80, 78, 71, 13, 10, 26, 10].every((value, index) => bytes[index] === value)) extension = "png";
  else if (bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255) extension = "jpg";
  else if (new TextDecoder().decode(bytes.slice(0, 4)) === "RIFF" && new TextDecoder().decode(bytes.slice(8, 12)) === "WEBP") extension = "webp";
  else throw new Error("This file is not a supported PNG, JPEG or WebP image.");
  const label = file.name.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]!);
  const path = `image.${extension}`;
  const html = `<!doctype html><html><head><meta charset="utf-8"><title>${label}</title><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><img data-component-id="project-image" src="${path}" alt="${label}"></body></html>`;
  return [{ path: "index.html", file: new File([html], "index.html", { type: "text/html" }) }, { path, file }];
}
