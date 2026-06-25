"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  CheckCircle2,
  ExternalLink,
  FolderUp,
  Loader2,
  UploadCloud,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import {
  collectDataTransferItems,
  collectInputFiles,
  deployFolder,
  type DeployProgress,
  type DroppedFile,
} from "@/lib/deploy";
import { cn } from "@/lib/utils";

type State =
  | { status: "idle" }
  | { status: "confirm"; files: DroppedFile[] }
  | { status: "working"; progress: DeployProgress; fileCount: number }
  | { status: "done"; liveUrl: string }
  | { status: "error"; message: string };

/**
 * The manifest key the serving Worker resolves the root URL ("/") to — exactly
 * this, lowercase, at the upload root. A deploy without it serves a 404 at "/",
 * which is the most common "my new site doesn't load" cause (usually a folder
 * dropped one level too deep). Mirrors the API's rootIndexFile check.
 */
const ROOT_INDEX_PATH = "index.html";

function hasRootIndex(files: DroppedFile[]): boolean {
  return files.some((f) => f.path === ROOT_INDEX_PATH);
}

/**
 * A single forward-moving percentage across the whole deploy, so the bar never
 * stalls or jumps backwards between phases (hash → prepare → upload → finalize →
 * publish). Hashing owns 0 to 40%, uploading 45 to 85%, the tail the rest.
 */
function overallPercent(p: DeployProgress): number {
  switch (p.phase) {
    case "hashing":
      return p.total ? (p.done / p.total) * 40 : 0;
    case "preparing":
      return 45;
    case "uploading":
      return 45 + (p.total ? (p.done / p.total) * 40 : 40);
    case "finalizing":
      return 90;
    case "publishing":
      return 96;
  }
}

function phaseLabel(p: DeployProgress): string {
  switch (p.phase) {
    case "hashing":
      return `Hashing files… ${p.done}/${p.total}`;
    case "preparing":
      return "Checking which files are new…";
    case "uploading":
      return p.total > 0
        ? `Uploading ${p.done}/${p.total} new files…`
        : "No new files to upload…";
    case "finalizing":
      return "Finalizing version…";
    case "publishing":
      return "Publishing…";
  }
}

/**
 * Folder drag-and-drop deploy. Drop a folder of static files
 * (or pick one) and it goes live: the browser hashes every file, uploads only the
 * blobs the server doesn't already have directly to object storage, then finalizes
 * + publishes. Same backend contract as `dropway deploy`, authed by your session.
 */
export function DeployDropzone({
  siteId,
  isLive,
}: {
  siteId: string;
  isLive: boolean;
}) {
  const router = useRouter();
  const inputRef = React.useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = React.useState(false);
  const [state, setState] = React.useState<State>({ status: "idle" });

  const working = state.status === "working";

  // <input webkitdirectory> turns the file picker into a folder picker. The attr
  // isn't in React's typed props, so set it imperatively (+ the legacy `directory`).
  React.useEffect(() => {
    const el = inputRef.current;
    if (el) {
      el.setAttribute("webkitdirectory", "");
      el.setAttribute("directory", "");
    }
  }, []);

  const runDeploy = React.useCallback(
    async (files: DroppedFile[]) => {
      setState({
        status: "working",
        progress: { phase: "hashing", done: 0, total: files.length },
        fileCount: files.length,
      });
      const outcome = await deployFolder({
        siteId,
        files,
        onProgress: (progress) =>
          setState((s) =>
            s.status === "working" ? { ...s, progress } : s,
          ),
      });
      if (outcome.ok) {
        setState({ status: "done", liveUrl: outcome.liveUrl });
        // Refresh the server component so the Live URL + Current version cards update.
        router.refresh();
      } else {
        setState({ status: "error", message: outcome.message });
      }
    },
    [siteId, router],
  );

  const start = React.useCallback(
    (files: DroppedFile[]) => {
      if (files.length === 0) {
        setState({
          status: "error",
          message:
            "No files found. Drop a folder of static files (with an index.html at its root).",
        });
        return;
      }
      // Warn (but don't block) when there's no root index.html: the site root
      // would 404. Catch it BEFORE hashing/uploading so the fix is a quick re-drop.
      if (!hasRootIndex(files)) {
        setState({ status: "confirm", files });
        return;
      }
      void runDeploy(files);
    },
    [runDeploy],
  );

  async function onDrop(e: React.DragEvent) {
    e.preventDefault();
    setDragging(false);
    if (working) return;
    // Capture entries synchronously inside the event before any await.
    const files = await collectDataTransferItems(e.dataTransfer.items);
    void start(files);
  }

  function onInputChange(e: React.ChangeEvent<HTMLInputElement>) {
    const list = e.target.files;
    if (list && list.length > 0) void start(collectInputFiles(list));
    // Reset so picking the same folder again re-fires change.
    e.target.value = "";
  }

  function openPicker() {
    if (!working) inputRef.current?.click();
  }

  // ---- result states ----
  if (state.status === "done") {
    return (
      <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4">
        <div className="flex items-start gap-3">
          <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-500" aria-hidden />
          <div className="min-w-0 flex-1 space-y-2">
            <p className="text-sm font-medium text-foreground">
              Deployed and live.
            </p>
            {state.liveUrl ? (
              <a
                href={state.liveUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex max-w-full items-center gap-2 truncate rounded-md border border-border bg-background px-3 py-1.5 font-mono text-xs text-foreground transition-colors hover:border-foreground/20"
              >
                <span className="truncate">{state.liveUrl}</span>
                <ExternalLink className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
              </a>
            ) : null}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setState({ status: "idle" })}
          >
            Deploy again
          </Button>
        </div>
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4">
        <div className="flex items-start gap-3">
          <AlertCircle className="mt-0.5 size-5 shrink-0 text-destructive" aria-hidden />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-foreground">Deploy failed</p>
            <p className="mt-1 text-sm text-muted-foreground">{state.message}</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setState({ status: "idle" })}
          >
            Try again
          </Button>
        </div>
      </div>
    );
  }

  if (state.status === "confirm") {
    const { files } = state;
    return (
      <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4">
        <div className="flex items-start gap-3">
          <AlertCircle className="mt-0.5 size-5 shrink-0 text-amber-500" aria-hidden />
          <div className="min-w-0 flex-1 space-y-1">
            <p className="text-sm font-medium text-foreground">
              No <code className="font-mono">index.html</code> at the folder root
            </p>
            <p className="text-sm text-muted-foreground">
              Your site&rsquo;s root URL (<code className="font-mono">/</code>) will
              return 404. If you dropped a folder that wraps your site, drop the{" "}
              <strong>inner</strong> folder instead — or rename your entry page to{" "}
              <code className="font-mono">index.html</code>. You can deploy anyway
              if the site only serves sub-paths.
            </p>
          </div>
          <div className="flex shrink-0 gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setState({ status: "idle" })}
            >
              Cancel
            </Button>
            <Button size="sm" onClick={() => void runDeploy(files)}>
              Deploy anyway
            </Button>
          </div>
        </div>
      </div>
    );
  }

  // ---- idle + working share the drop surface ----
  return (
    <div className="space-y-3">
      <div
        role="button"
        tabIndex={working ? -1 : 0}
        aria-disabled={working}
        aria-label="Drag and drop a website folder to deploy, or activate to choose a folder"
        onClick={openPicker}
        onKeyDown={(e) => {
          if (!working && (e.key === "Enter" || e.key === " ")) {
            e.preventDefault();
            openPicker();
          }
        }}
        onDragOver={(e) => {
          e.preventDefault();
          if (!working) setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        className={cn(
          "flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-6 py-10 text-center transition-colors",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          working
            ? "cursor-default border-border bg-muted/40"
            : dragging
              ? "cursor-pointer border-primary bg-primary/5"
              : "cursor-pointer border-border hover:border-foreground/25 hover:bg-muted/40",
        )}
      >
        {working ? (
          <Loader2 className="size-7 animate-spin text-muted-foreground" aria-hidden />
        ) : (
          <span className="grid size-12 place-items-center rounded-full bg-secondary text-secondary-foreground">
            <UploadCloud className="size-6" aria-hidden />
          </span>
        )}

        {working ? (
          <div className="w-full max-w-sm space-y-2">
            <Progress value={overallPercent(state.progress)} />
            <p className="text-sm text-muted-foreground">
              {phaseLabel(state.progress)}
            </p>
          </div>
        ) : (
          <>
            <div className="space-y-1">
              <p className="text-sm font-medium text-foreground">
                Drag &amp; drop your website folder here
              </p>
              <p className="text-xs text-muted-foreground">
                Static files only. Make sure there&rsquo;s an{" "}
                <code className="font-mono">index.html</code> at the folder root.
                {isLive ? " This replaces the live version instantly." : ""}
              </p>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={(e) => { e.stopPropagation(); openPicker(); }}>
              <FolderUp aria-hidden />
              Choose folder
            </Button>
          </>
        )}
      </div>

      <input
        ref={inputRef}
        type="file"
        multiple
        className="hidden"
        onChange={onInputChange}
        // a11y: the drop surface above is the labelled control; this is its picker.
        tabIndex={-1}
        aria-hidden
      />
    </div>
  );
}
