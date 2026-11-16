import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TaskList, TaskSummary, TaskEvents, formatLocalTaskTime } from "@/components/project-room/task-activity";
import type { HostedJob } from "@/lib/project-room/hosted";
const job: HostedJob = {
  id: "task", roomId: "room", siteId: "site", versionId: "v1", contentHash: "hash", initiatorUserId: "owner",
  state: "failed", sessionId: null, candidateId: null, failureCode: "usage_unverified", tokensUsed: 13465, callsUsed: 1,
  config: {provider: "anthropic", modelId: "claude-opus-4-1", harnessId: "pi", harnessVersion: "1.5.0", prompt: "Remove this line from the site", threadId: null, budget: {max_tokens: 20000, max_calls: 10, max_seconds: 300, max_output_bytes: 100000}},
};
describe("AI task activity", () => {
  it("shows selectable task context rather than a plain status link", () => {
    const html = renderToStaticMarkup(createElement(TaskList, {jobs: [job], selectedId: job.id, onSelect() {}}));
    expect(html).toContain('aria-pressed="true"');
    for (const text of [job.config.prompt, "Failed", job.config.modelId, "13,465 tokens"]) expect(html).toContain(text);
  });
  it("keeps failure guidance and accounted usage visible in task details", () => {
    const html = renderToStaticMarkup(createElement(TaskSummary, {job}));
    expect(html).toContain("Provider usage could not be verified");
    expect(html).toContain("Accounted tokens");
    expect(html).toContain("20,000");
  });
  it("provides empty states and readable, timezone-explicit progress", () => {
    expect(renderToStaticMarkup(createElement(TaskList, {jobs: [], selectedId: "", onSelect() {}}))).toContain("No AI tasks yet");
    const html = renderToStaticMarkup(createElement(TaskEvents, {events: [{id: "event", kind: "progress", message: "Preparing isolated revision", createdAt: "2026-11-15T20:14:50.960282Z"}]}));
    expect(html).toContain('dateTime="2026-11-15T20:14:50.960282Z"');
    expect(html).not.toContain(" UTC");
    expect(html).toContain("Preparing isolated revision");
    expect(html).toContain("<summary");
  });
});

it("orders tasks newest first and includes request and outcome timestamps", () => {
  const older={...job,id:"old",createdAt:"2026-11-15T10:00:00Z",updatedAt:"2026-11-15T10:01:00Z",config:{...job.config,prompt:"Older task"}};
  const newer={...job,id:"new",createdAt:"2026-11-15T12:00:00Z",updatedAt:"2026-11-15T12:01:00Z",config:{...job.config,prompt:"Newer task"}};
  const html=renderToStaticMarkup(createElement(TaskList,{jobs:[older,newer],selectedId:"",onSelect(){}}));
  expect(html.indexOf("Newer task")).toBeLessThan(html.indexOf("Older task"));
  expect(html).toContain('dateTime="2026-11-15T12:01:00Z"');
  expect(html).toContain('dateTime="2026-11-15T10:00:00Z"');
  expect(html).not.toContain('grid-cols-2');
});

it("uses the runtime local timezone instead of forcing UTC", () => {
  const original=process.env.TZ;
  try {
    process.env.TZ="America/Toronto";
    const toronto=formatLocalTaskTime("2026-11-15T20:00:00Z");
    process.env.TZ="Asia/Tokyo";
    const tokyo=formatLocalTaskTime("2026-11-15T20:00:00Z");
    expect(toronto).not.toBe(tokyo);
    expect(formatLocalTaskTime("invalid")).toBe("Time unavailable");
  } finally { if(original===undefined) delete process.env.TZ; else process.env.TZ=original; }
});
