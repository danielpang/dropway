"use client";
import { useEffect, useState } from "react";
import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { loadCandidatePreviewAction } from "@/app/(app)/rooms/preview-actions";
import type { ReviewCandidate } from "@/lib/project-room/contract";

type Preview = Extract<
  Awaited<ReturnType<typeof loadCandidatePreviewAction>>,
  { ok: true }
>["preview"];
export function VisualComparison({
  candidate,
  onReady,
  loader = loadCandidatePreviewAction,
}: {
  candidate: ReviewCandidate;
  onReady: (ready: boolean) => void;
  loader?: typeof loadCandidatePreviewAction;
}) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [path, setPath] = useState<string>();
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    setPreview(null);
    setError("");
    onReady(false);
    void loader(candidate, path)
      .then((result) => {
        if (!active) return;
        if (result.ok) {
          setPreview(result.preview);
          onReady(true);
        } else setError(result.message);
      })
      .catch(() => {
        if (active) setError("We couldn’t load this comparison. Try again.");
      });
    return () => {
      active = false;
    };
  }, [
    candidate.ID,
    candidate.ContentHash,
    candidate.BaseContentHash,
    path,
    retry,
    onReady,
    loader,
  ]);
  return (
    <section
      aria-label="Visual comparison"
      className="overflow-hidden rounded-lg border border-border"
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border bg-muted/30 px-4 py-3">
        <p className="text-sm font-medium">Compare changes</p>
        {preview && preview.pages.length > 1 ? (
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            Page or image
            <Select
              aria-label="Preview page"
              className="max-w-72"
              value={preview.path}
              onChange={(e) => setPath(e.target.value)}
            >
              {preview.pages.map((p) => (
                <option key={p.path} value={p.path}>
                  {p.label}
                </option>
              ))}
            </Select>
          </label>
        ) : null}
      </div>
      {error ? (
        <div className="space-y-3 p-8 text-center">
          <p role="alert" className="text-sm text-muted-foreground">
            {error}
          </p>
          <Button variant="outline" onClick={() => setRetry((n) => n + 1)}>
            <RotateCcw className="size-4" />
            Retry preview
          </Button>
        </div>
      ) : !preview ? (
        <p
          role="status"
          className="p-12 text-center text-sm text-muted-foreground"
        >
          Loading previews…
        </p>
      ) : (
        <ComparisonPanes before={preview.before} after={preview.after} />
      )}
      <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
        Previews show saved content and styling. Interactive features and
        external assets aren’t shown.
      </p>
    </section>
  );
}
export function ComparisonPanes({
  before,
  after,
}: {
  before: string | null;
  after: string | null;
}) {
  return (
    <div className="overflow-x-auto">
      <div className="grid min-w-[640px] grid-cols-2 divide-x divide-border">
        {[
          { label: "Before", hint: "Original version", html: before },
          { label: "After", hint: "Proposed change", html: after },
        ].map((side) => (
          <div key={side.label} className="min-w-0">
            <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
              <span className="text-sm font-medium">{side.label}</span>
              <span className="text-xs text-muted-foreground">{side.hint}</span>
            </div>
            {side.html ? (
              <iframe
                title={`${side.label} preview`}
                srcDoc={side.html}
                sandbox=""
                referrerPolicy="no-referrer"
                className="h-[min(68vh,720px)] min-h-96 w-full border-0 bg-white"
              />
            ) : (
              <div className="grid min-h-96 place-items-center p-8 text-sm text-muted-foreground">
                {side.label === "Before"
                  ? "New in this change"
                  : "Removed in this change"}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
