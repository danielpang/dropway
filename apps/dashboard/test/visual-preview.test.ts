import { createHash } from "node:crypto";
import { expect, it } from "vitest";
import {
  renderComparisonPage,
  buildVisualComparison,
} from "@/lib/project-room/visual-preview";
import type { ReviewCandidate } from "@/lib/project-room/contract";
export const previewCandidate: ReviewCandidate = {
  ID: "c",
  RoomID: "r",
  SiteID: "s",
  BaseVersionID: "v",
  BaseContentHash: "base",
  ContentHash: "next",
  DiffHash: "diff",
  Summary: "Removed the highlighted line.",
  State: "pending",
  CreatedBy: "u",
  CreatedAt: "",
};
export function artifact(path: string, content: string | Buffer) {
  const data = Buffer.from(content);
  return {
    path,
    data: data.toString("base64"),
    sha256: createHash("sha256").update(data).digest("hex"),
  };
}
it("renders complete styled pages with local assets and distinct before/after", () => {
  const pixel = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=",
    "base64",
  );
  const shared = [
    artifact(
      "style.css",
      'h1{color:rgb(200, 0, 0)} body{background-image:url("./photo.png")}',
    ),
    artifact("photo.png", pixel),
  ];
  const data = {
    candidate_id: "c",
    base_content_hash: "base",
    content_hash: "next",
    before: [
      artifact(
        "index.html",
        '<link rel="stylesheet" href="style.css"><h1>Before</h1><p>Remove me</p>',
      ),
      ...shared,
    ],
    after: [
      artifact(
        "index.html",
        '<link rel="stylesheet" href="style.css"><h1>After</h1>',
      ),
      ...shared,
    ],
  };
  const result = renderComparisonPage(data, previewCandidate, "index.html");
  expect(result.before).toContain("Remove me");
  expect(result.after).not.toContain("Remove me");
  expect(result.after).toContain("color:rgb(200, 0, 0)");
  expect(result.after).toContain("data:image/png;base64,");
  expect(result.pages[0]?.label).toBe("Home page");
});
it("removes executable markup, navigation and remote asset references", () => {
  const data = {
    candidate_id: "c",
    base_content_hash: "base",
    content_hash: "next",
    before: [],
    after: [
      artifact(
        "index.html",
        '<meta http-equiv="refresh" content="0;url=https://evil.test"><script>attack()</script><iframe src="https://evil.test"></iframe><a href="https://evil.test" onclick="attack()">Link</a><img src="https://evil.test/img"><style>body{background:url(https://evil.test/bg)}</style>',
      ),
    ],
  };
  const result = renderComparisonPage(data, previewCandidate, "index.html");
  expect(result.before).toBeNull();
  expect(result.after).not.toContain("evil.test");
  expect(result.after).not.toContain("attack()");
  expect(result.after).toContain("script-src &#39;none&#39;");
});
it("rejects altered bytes and mismatched proposals", () => {
  const data = {
    candidate_id: "other",
    base_content_hash: "base",
    content_hash: "next",
    before: [],
    after: [],
  };
  expect(() => buildVisualComparison(data, previewCandidate)).toThrow();
  expect(() =>
    buildVisualComparison(
      {
        ...data,
        candidate_id: "c",
        after: [
          {
            ...artifact("index.html", "good"),
            data: Buffer.from("bad").toString("base64"),
          },
        ],
      },
      previewCandidate,
    ),
  ).toThrow();
});
it("previews added and removed images without code or hashes", () => {
  const data = {
    candidate_id: "c",
    base_content_hash: "base",
    content_hash: "next",
    before: [artifact("old.png", "pixels")],
    after: [artifact("new.png", "pixels")],
  };
  expect(
    renderComparisonPage(data, previewCandidate, "new.png").before,
  ).toBeNull();
  expect(
    renderComparisonPage(data, previewCandidate, "old.png").after,
  ).toBeNull();
});

it("opens the changed page first and omits unchanged image-only entries", () => {
  const shared = [
    artifact("index.html", "Home"),
    artifact("logo.png", "pixels"),
  ];
  const comparison = buildVisualComparison(
    {
      candidate_id: "c",
      base_content_hash: "base",
      content_hash: "next",
      before: [...shared, artifact("about/index.html", "Old about page")],
      after: [...shared, artifact("about/index.html", "New about page")],
    },
    previewCandidate,
  );
  expect(comparison.pages.map((page) => page.path)).toEqual([
    "about/index.html",
    "index.html",
  ]);
});

it("keeps nested stylesheet images and rejects oversized source pages", () => {
  const after = [
    artifact(
      "index.html",
      '<link rel="stylesheet" href="css/main.css"><h1>Title</h1>',
    ),
    artifact("css/main.css", '@import "colors.css";'),
    artifact("css/colors.css", 'body{background:url("../photo.png")}'),
    artifact("photo.png", "pixels"),
  ];
  const data = {
    candidate_id: "c",
    base_content_hash: "base",
    content_hash: "next",
    before: [],
    after,
  };
  expect(
    renderComparisonPage(data, previewCandidate, "index.html").after,
  ).toContain("data:image/png;base64,");
  expect(() =>
    renderComparisonPage(
      { ...data, after: [artifact("index.html", "x".repeat(513 * 1024))] },
      previewCandidate,
      "index.html",
    ),
  ).toThrow("Page too large");
});
