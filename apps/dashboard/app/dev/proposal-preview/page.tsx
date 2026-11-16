import { notFound } from "next/navigation";
import { createHash } from "node:crypto";
import { renderComparisonPage } from "@/lib/project-room/visual-preview";
import { ProposalDemo } from "./preview";
export const dynamic = "force-dynamic";
export default function Page() {
  if (process.env.NODE_ENV !== "development") notFound();
  const candidate = {
    ID: "demo",
    RoomID: "room",
    SiteID: "site",
    BaseVersionID: "v1",
    BaseContentHash: "base",
    ContentHash: "new",
    DiffHash: "diff",
    Summary:
      "Removed “The only ask is to say hello” from the home page.\n\nThe heading, image and other three points stay the same.",
    State: "pending",
    CreatedBy: "demo",
    CreatedAt: "",
  };
  const html = (extra: string) =>
    `<!doctype html><html><head><style>body{margin:0;background:#f5f2eb;color:#233b32;font:17px/1.7 system-ui;padding:40px}small{font-size:13px;color:#69786b}h1{font:48px/1.1 Georgia;margin:24px 0}section{border-top:1px solid #adb9ab;padding-top:24px;margin-top:32px}a{display:inline-block;background:#233b32;color:white;padding:12px 24px;border-radius:6px}li{margin:8px 0}</style></head><body><small>CHRISTCHURCH · RICHMOND HILL</small><h1>A place to belong.</h1><p>Come as you are. Find a community that feels like home.</p><section><h2>Site A: Minimal</h2><ul><li>Written for neighbours who are not Christians</li><li>One typeface, one photo, very little colour</li><li>Answers the honest questions a newcomer would ask</li>${extra}</ul><a>View Site A</a></section></body></html>`;
  const file = (body: string) => ({
    path: "index.html",
    data: Buffer.from(body).toString("base64"),
    sha256: createHash("sha256").update(body).digest("hex"),
  });
  const preview = renderComparisonPage(
    {
      candidate_id: "demo",
      base_content_hash: "base",
      content_hash: "new",
      before: [file(html("<li>The only ask is to say hello</li>"))],
      after: [file(html(""))],
    },
    candidate,
    "index.html",
  );
  return <ProposalDemo candidate={candidate} preview={preview} />;
}
