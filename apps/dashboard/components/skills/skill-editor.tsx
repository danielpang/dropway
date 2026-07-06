"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Eye, Loader2, Pencil, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import type { SkillFolder } from "@/lib/api";
import { renderMarkdownToHtml } from "@/lib/markdown";
import {
  composeSkillFiles,
  skillTemplate,
  type AuthoredFile,
} from "@/lib/skill-authoring";
import { slugify } from "@/lib/skills-shared";
import { uploadSkillFolder, type SkillUploadProgress } from "@/lib/skill-upload";

function progressLabel(p: SkillUploadProgress | null): string {
  switch (p?.phase) {
    case "hashing":
      return "Preparing…";
    case "creating":
      return "Creating the skill…";
    case "preparing":
      return "Preparing upload…";
    case "uploading":
      return `Uploading… ${p.done}/${p.total}`;
    case "finalizing":
      return "Publishing…";
    default:
      return "";
  }
}

function progressValue(p: SkillUploadProgress | null): number {
  switch (p?.phase) {
    case "hashing":
      return 15;
    case "creating":
      return 35;
    case "preparing":
      return 50;
    case "uploading":
      return 70;
    case "finalizing":
      return 95;
    default:
      return 0;
  }
}

/**
 * Author a skill in the browser: a name + folders, a Markdown editor for SKILL.md
 * (with a live preview), and optional extra files. On save it composes the files
 * and runs them through the same upload pipeline as drag-and-drop, then opens the
 * new skill's page.
 */
export function SkillEditor(props: { folders: SkillFolder[] }) {
  const { folders } = props;
  const router = useRouter();

  const [name, setName] = React.useState("");
  const [selected, setSelected] = React.useState<Set<string>>(new Set());
  const [skillMd, setSkillMd] = React.useState(() => skillTemplate(""));
  const [extras, setExtras] = React.useState<AuthoredFile[]>([]);
  const [mode, setMode] = React.useState<"edit" | "preview">("edit");
  const [uploading, setUploading] = React.useState(false);
  const [progress, setProgress] = React.useState<SkillUploadProgress | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [warnings, setWarnings] = React.useState<string[]>([]);
  const [createdSkillId, setCreatedSkillId] = React.useState<string | null>(null);
  // Whether the body still equals the auto-scaffold (so we can refresh the
  // template's title as the user types a name, without clobbering their edits).
  const templateRef = React.useRef(skillTemplate(""));

  const slug = slugify(name);

  const onNameChange = (value: string) => {
    setName(value);
    if (skillMd === templateRef.current) {
      const next = skillTemplate(value);
      templateRef.current = next;
      setSkillMd(next);
    }
  };

  const setExtra = (i: number, patch: Partial<AuthoredFile>) =>
    setExtras((prev) => prev.map((e, idx) => (idx === i ? { ...e, ...patch } : e)));

  const save = async () => {
    setError(null);
    setWarnings([]);
    if (!slug) {
      setError("Give the skill a name.");
      return;
    }
    const composed = composeSkillFiles(skillMd, extras);
    if (composed.error) {
      setError(composed.error);
      return;
    }
    setUploading(true);
    const res = await uploadSkillFolder({
      skillId: createdSkillId,
      create: { slug, title: name.trim() || undefined, folders: Array.from(selected) },
      files: composed.files,
      onProgress: setProgress,
    });
    setUploading(false);
    if (res.skillId) setCreatedSkillId(res.skillId);
    if (!res.ok) {
      setError(res.message);
      setProgress(null);
      return;
    }
    if (res.warnings.length > 0) {
      setWarnings(res.warnings);
    }
    router.push(`/skills/${res.skillId}`);
    router.refresh();
  };

  return (
    <div className="space-y-5">
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="skill-name">Name</Label>
          <Input
            id="skill-name"
            value={name}
            onChange={(e) => onNameChange(e.target.value)}
            placeholder="PR review checklist"
          />
          {slug ? (
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
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between">
          <Label htmlFor="skill-md">
            SKILL.md <span className="text-muted-foreground">(Markdown)</span>
          </Label>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant={mode === "edit" ? "secondary" : "ghost"}
              size="sm"
              onClick={() => setMode("edit")}
            >
              <Pencil className="mr-1 h-3.5 w-3.5" /> Edit
            </Button>
            <Button
              type="button"
              variant={mode === "preview" ? "secondary" : "ghost"}
              size="sm"
              onClick={() => setMode("preview")}
            >
              <Eye className="mr-1 h-3.5 w-3.5" /> Preview
            </Button>
          </div>
        </div>
        {mode === "edit" ? (
          <textarea
            id="skill-md"
            value={skillMd}
            onChange={(e) => setSkillMd(e.target.value)}
            spellCheck
            className="min-h-[24rem] w-full resize-y rounded-md border border-border bg-background p-3 font-mono text-sm leading-relaxed outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        ) : (
          <Card className="min-h-[24rem] p-4">
            <div
              className="skill-markdown space-y-3 text-sm leading-relaxed"
              dangerouslySetInnerHTML={{ __html: renderMarkdownToHtml(skillMd) }}
            />
          </Card>
        )}
        <p className="text-xs text-muted-foreground">
          Keep the YAML frontmatter (<code>name</code> / <code>description</code>) at the top — it
          describes the skill to assistants.
        </p>
      </div>

      {/* Optional supporting files. */}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <Label>Additional files</Label>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setExtras((prev) => [...prev, { path: "", content: "" }])}
          >
            <Plus className="mr-1 h-3.5 w-3.5" /> Add file
          </Button>
        </div>
        {extras.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            Optional. Add reference files (e.g. <code>references/checklist.md</code>) the skill
            should ship with.
          </p>
        ) : (
          extras.map((extra, i) => (
            <Card key={i} className="space-y-2 p-3">
              <div className="flex items-center gap-2">
                <Input
                  value={extra.path}
                  onChange={(e) => setExtra(i, { path: e.target.value })}
                  placeholder="references/checklist.md"
                  aria-label="File path"
                  className="font-mono text-xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  title="Remove file"
                  onClick={() => setExtras((prev) => prev.filter((_, idx) => idx !== i))}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
              <textarea
                value={extra.content}
                onChange={(e) => setExtra(i, { content: e.target.value })}
                placeholder="File contents…"
                className="min-h-[8rem] w-full resize-y rounded-md border border-border bg-background p-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </Card>
          ))
        )}
      </div>

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

      <div className="flex items-center justify-end gap-2">
        <Button variant="outline" disabled={uploading} onClick={() => router.push("/skills")}>
          Cancel
        </Button>
        <Button disabled={uploading || !slug} onClick={() => void save()}>
          {uploading ? <Loader2 className="mr-1.5 h-4 w-4 animate-spin" /> : null}
          Create skill
        </Button>
      </div>
    </div>
  );
}
