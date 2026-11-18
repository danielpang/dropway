"use client";
import { useEffect, useState } from "react";
import { LoaderCircle, RotateCcw } from "lucide-react";
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
  const [pages, setPages] = useState<Preview["pages"]>([]);
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
          setPages(result.preview.pages);
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
        {pages.length > 1 ? (
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            Page or image
            <Select
              aria-label="Preview page"
              className="max-w-72"
              value={path ?? preview?.path ?? pages[0]?.path}
              onChange={(e) => {
                if (e.target.value === (path ?? preview?.path)) return;
                onReady(false);
                setPreview(null);
                setPath(e.target.value);
              }}
            >
              {pages.map((p) => (
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
          className="flex min-h-80 items-center justify-center gap-2 bg-muted/20 p-8 text-sm text-muted-foreground"
        >
          <LoaderCircle className="size-4 motion-safe:animate-spin" aria-hidden="true" />
          Loading previews…
        </p>
      ) : (
        <ComparisonPanes key={preview.path} before={preview.before} after={preview.after} onReady={onReady} />
      )}
      <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
        Previews show saved content and styling. Interactive features and
        external assets aren’t shown.
      </p>
    </section>
  );
}
function ComparisonPanes({
  before,
  after,
  onReady,
}: {
  before: string | null;
  after: string | null;
  onReady: (ready: boolean) => void;
}) {
  const [loaded, setLoaded] = useState({ Before: false, After: false });
  useEffect(() => {
    onReady((before === null || loaded.Before) && (after === null || loaded.After));
  }, [before, after, loaded, onReady]);
  return (
    <div className="grid divide-y divide-border md:grid-cols-2 md:divide-x md:divide-y-0">
      {([
        { label: "Before", hint: "Original version", html: before },
        { label: "After", hint: "Proposed change", html: after },
      ] as const).map(({ label, hint, html }) => (
        <section key={label} aria-label={hint} className="min-w-0">
          <div className="flex items-baseline justify-between gap-2 border-b border-border bg-muted/30 px-4 py-2">
            <h3 className="text-sm font-semibold">{label}</h3>
            <span className="text-xs text-muted-foreground">{hint}</span>
          </div>
          {html !== null ? (
            <div className="relative">
              <iframe title={label + " preview"} srcDoc={html} sandbox="" referrerPolicy="no-referrer"
                onLoad={() => setLoaded((previous) => ({ ...previous, [label]: true }))}
                className="block h-[44vh] min-h-56 w-full border-0 bg-white md:h-[min(68vh,720px)]" />
              {!loaded[label] && <p role="status" className="absolute inset-0 grid place-items-center bg-muted text-sm text-muted-foreground">Opening {hint.toLowerCase()}…</p>}
            </div>
          ) : (
            <p className="grid min-h-56 place-items-center bg-muted/20 p-8 text-sm text-muted-foreground">
              {label === "Before" ? "New in this change" : "Removed in this change"}
            </p>
          )}
        </section>
      ))}
    </div>
  );
}
