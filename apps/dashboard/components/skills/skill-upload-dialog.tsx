"use client";

import * as React from "react";
import { FolderUp, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import type { SkillFolder } from "@/lib/api";
import {
  collectDataTransferItems,
  collectInputFiles,
  dropRootName,
  inputRootName,
  type DroppedFile,
} from "@/lib/deploy-manifest";
import {
  precheckSkillFolder,
  uploadSkillFolder,
  type SkillUploadProgress,
} from "@/lib/skill-upload";

/** Slugify a folder name the way the CLI does: lowercase DNS-label-ish. */
function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

function progressLabel(p: SkillUploadProgress | null): string {
  switch (p?.phase) {
    case "hashing":
      return `Hashing files… ${p.done}/${p.total}`;
    case "creating":
      return "Creating the skill…";
    case "preparing":
      return "Preparing upload…";
    case "uploading":
      return `Uploading… ${p.done}/${p.total}`;
    case "finalizing":
      return "Finalizing…";
    default:
      return "";
  }
}

function progressValue(p: SkillUploadProgress | null): number {
  switch (p?.phase) {
    case "hashing":
      return p.total ? (p.done / p.total) * 30 : 10;
    case "creating":
      return 35;
    case "preparing":
      return 40;
    case "uploading":
      return 45 + (p.total ? (p.done / p.total) * 45 : 45);
    case "finalizing":
      return 95;
    default:
      return 0;
  }
}

/**
 * Upload dialog: drop (or pick) a skill folder — a directory with a SKILL.md at
 * its root — name it, choose folders, and share it with the org. Reuses the
 * deploy manifest/hash machinery; only the JSON steps hit the API (via server
 * actions), the bytes go straight to storage.
 */
export function SkillUploadDialog(props: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  folders: SkillFolder[];
  onDone: () => void;
}) {
  const { open, onOpenChange, folders, onDone } = props;
  const [files, setFiles] = React.useState<DroppedFile[]>([]);
  const [folderName, setFolderName] = React.useState("");
  // nameInput is the raw text the user types; the slug is derived from it only
  // at submit (slugify-on-every-keystroke made trailing separators impossible to
  // type, so "pr review" collapsed to "prreview").
  const [nameInput, setNameInput] = React.useState("");
  const [selected, setSelected] = React.useState<Set<string>>(new Set());
  const [dragOver, setDragOver] = React.useState(false);
  const [progress, setProgress] = React.useState<SkillUploadProgress | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [warnings, setWarnings] = React.useState<string[]>([]);
  const [uploading, setUploading] = React.useState(false);
  // Preserves the created skill id across retries so a failure after the create
  // step reuses the same skill instead of dead-ending on "slug already in use".
  const [createdSkillId, setCreatedSkillId] = React.useState<string | null>(null);
  const inputRef = React.useRef<HTMLInputElement>(null);

  const slug = slugify(nameInput);

  const reset = () => {
    setFiles([]);
    setFolderName("");
    setNameInput("");
    setSelected(new Set());
    setProgress(null);
    setError(null);
    setWarnings([]);
    setUploading(false);
    setCreatedSkillId(null);
  };

  const accept = (dropped: DroppedFile[], name: string) => {
    setError(precheckSkillFolder(dropped));
    setFiles(dropped);
    setFolderName(name);
    // A fresh folder means a fresh skill; drop any id from a prior attempt.
    setCreatedSkillId(null);
    if (!nameInput.trim()) setNameInput(name);
  };

  const start = async () => {
    setUploading(true);
    setError(null);
    setWarnings([]);
    const res = await uploadSkillFolder({
      skillId: createdSkillId,
      create: { slug, folders: Array.from(selected) },
      files,
      onProgress: setProgress,
    });
    setUploading(false);
    // Remember the id whenever we have one, so "Try again" reuses the skill.
    if (res.skillId) setCreatedSkillId(res.skillId);
    if (!res.ok) {
      setError(res.message);
      setProgress(null);
      return;
    }
    setWarnings(res.warnings);
    onDone();
    if (res.warnings.length === 0) {
      reset();
      onOpenChange(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (uploading) return; // don't tear down mid-upload
        if (!next) reset();
        onOpenChange(next);
      }}
      className="max-w-lg"
    >
      <DialogHeader>
        <DialogTitle>Upload a skill</DialogTitle>
      </DialogHeader>
      <DialogBody className="space-y-4">
        <div
          role="button"
          tabIndex={0}
          aria-label="Drop a skill folder or click to pick one"
          onClick={() => inputRef.current?.click()}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") inputRef.current?.click();
          }}
          onDragOver={(e) => {
            e.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragOver(false);
            // Read the dropped folder's own name synchronously (entries are only
            // valid during the event) — the collector strips it from paths.
            const root = dropRootName(e.dataTransfer.items) || "skill";
            void collectDataTransferItems(e.dataTransfer.items).then((dropped) => {
              accept(dropped, root);
            });
          }}
          className={`flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-8 text-center text-sm transition-colors ${
            dragOver ? "border-primary bg-primary/5" : "border-border text-muted-foreground"
          }`}
        >
          <FolderUp className="h-6 w-6" />
          {files.length > 0 ? (
            <span>
              <strong>{folderName || "folder"}</strong> — {files.length} file
              {files.length === 1 ? "" : "s"} ready
            </span>
          ) : (
            <span>
              Drop a skill folder here (it needs a <code>SKILL.md</code> at the top level), or
              click to pick one.
            </span>
          )}
          <input
            ref={inputRef}
            type="file"
            className="hidden"
            // @ts-expect-error webkitdirectory is a de-facto standard attribute.
            webkitdirectory=""
            multiple
            onChange={(e) => {
              if (!e.target.files?.length) return;
              const root = inputRootName(e.target.files) || "skill";
              const dropped = collectInputFiles(e.target.files);
              accept(dropped, root);
            }}
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="skill-slug">Skill name</Label>
          <Input
            id="skill-slug"
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            placeholder="pr-review-checklist"
          />
          {slug && slug !== nameInput.trim() ? (
            <p className="text-xs text-muted-foreground">
              Saved as <code>{slug}</code>
            </p>
          ) : null}
        </div>

        {folders.length > 0 ? (
          <div className="space-y-1.5">
            <Label>Folders</Label>
            <div className="flex flex-wrap gap-2">
              {folders.map((f) => {
                const on = selected.has(f.id ?? "");
                return (
                  <Button
                    key={f.id}
                    type="button"
                    variant={on ? "secondary" : "outline"}
                    size="sm"
                    aria-pressed={on}
                    onClick={() => {
                      const next = new Set(selected);
                      if (on) next.delete(f.id ?? "");
                      else next.add(f.id ?? "");
                      setSelected(next);
                    }}
                  >
                    {f.title}
                  </Button>
                );
              })}
            </div>
          </div>
        ) : null}

        {uploading || progress ? (
          <div className="space-y-1.5">
            <Progress value={progressValue(progress)} />
            <p className="text-xs text-muted-foreground">{progressLabel(progress)}</p>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        {warnings.map((w) => (
          <p key={w} className="text-sm text-amber-600 dark:text-amber-400">
            {w}
          </p>
        ))}
      </DialogBody>
      <DialogFooter>
        <Button variant="outline" disabled={uploading} onClick={() => onOpenChange(false)}>
          Cancel
        </Button>
        <Button
          disabled={uploading || files.length === 0 || !slug || precheckSkillFolder(files) !== null}
          onClick={() => void start()}
        >
          {uploading ? <Loader2 className="mr-1.5 h-4 w-4 animate-spin" /> : null}
          Share with the org
        </Button>
      </DialogFooter>
    </Dialog>
  );
}
