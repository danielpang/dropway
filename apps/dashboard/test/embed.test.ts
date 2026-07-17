import { describe, expect, it } from "vitest";

import { buildEmbedSnippet, buildEmbedUrl } from "@/lib/embed";
import { embedBadgeRemovable } from "@/lib/billing";

describe("buildEmbedUrl", () => {
  it("appends ?embed=1 to a bare site origin", () => {
    expect(buildEmbedUrl("https://acme.dropwaycontent.com", false)).toBe(
      "https://acme.dropwaycontent.com/?embed=1",
    );
  });

  it("adds &badge=0 only when removing the badge", () => {
    expect(buildEmbedUrl("https://acme.dropwaycontent.com", true)).toContain("badge=0");
    expect(buildEmbedUrl("https://acme.dropwaycontent.com", false)).not.toContain("badge");
  });

  it("preserves a custom domain and an existing path", () => {
    expect(buildEmbedUrl("https://docs.acme.com/guide", false)).toBe(
      "https://docs.acme.com/guide?embed=1",
    );
  });

  it("does not throw on a non-URL string (falls back to concatenation)", () => {
    expect(buildEmbedUrl("not a url", false)).toBe("not a url?embed=1");
    expect(buildEmbedUrl("has?query", true)).toBe("has?query&embed=1&badge=0");
  });
});

describe("buildEmbedSnippet", () => {
  const url = "https://acme.dropwaycontent.com/?embed=1";

  it("uses the given width/height", () => {
    const s = buildEmbedSnippet(url, "800", "400", "acme");
    expect(s).toContain(`src="${url}"`);
    expect(s).toContain('width="800"');
    expect(s).toContain('height="400"');
    expect(s).toContain('loading="lazy"');
    expect(s).toContain("max-width:100%");
  });

  it("falls back to 100% width and 600px height", () => {
    const s = buildEmbedSnippet(url, "  ", "", "acme");
    expect(s).toContain('width="100%"');
    expect(s).toContain('height="600"');
  });

  it("escapes the title attribute", () => {
    const s = buildEmbedSnippet(url, "100%", "600", 'a"b<c');
    expect(s).toContain('title="a&quot;b&lt;c"');
  });
});

describe("embedBadgeRemovable", () => {
  it("is false on free, true on every paid tier", () => {
    expect(embedBadgeRemovable("free")).toBe(false);
    expect(embedBadgeRemovable("pro")).toBe(true);
    expect(embedBadgeRemovable("business")).toBe(true);
    expect(embedBadgeRemovable("enterprise")).toBe(true);
  });
});
