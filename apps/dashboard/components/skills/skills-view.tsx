"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Download,
  FolderPlus,
  Loader2,
  Pencil,
  Search,
  Sparkles,
  Trash2,
} from "lucide-react";

import {
  addSkillToFolderAction,
  createSkillFolderAction,
  deleteSkillAction,
  deleteSkillFolderAction,
  downloadSkillAction,
  downloadSkillFolderAction,
  removeSkillFromFolderAction,
  renameSkillFolderAction,
  setPresetFlagAction,
} from "@/app/(app)/skills/actions";
import { SkillUploadDialog } from "@/components/skills/skill-upload-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogBody, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import type { Skill, SkillDownload, SkillFolder } from "@/lib/api";
import { isSafeSkillPath } from "@/lib/skills-shared";
import { buildZip, type ZipEntry } from "@/lib/zip";

// ---- Download helpers -------------------------------------------------------

// decodeSkillFiles returns the zip entries plus the paths it had to skip (only
// genuinely-unsafe paths, per isSafeSkillPath — a filename containing ".." is
// fine), so the caller can warn rather than silently omit files.
function decodeSkillFiles(
  download: SkillDownload,
  prefix: string,
): { entries: ZipEntry[]; skipped: string[] } {
  const entries: ZipEntry[] = [];
  const skipped: string[] = [];
  for (const f of download.files ?? []) {
    if (!f.path) continue;
    // Defense in depth: never let a path escape the target folder.
    if (!isSafeSkillPath(f.path)) {
      skipped.push(f.path);
      continue;
    }
    const data =
      f.encoding === "base64"
        ? Uint8Array.from(atob(f.content ?? ""), (c) => c.charCodeAt(0))
        : new TextEncoder().encode(f.content ?? "");
    entries.push({ path: `${prefix}${f.path}`, data });
  }
  return { entries, skipped };
}

function saveZip(name: string, entries: ZipEntry[]) {
  const bytes = buildZip(entries);
  // Copy into a fresh ArrayBuffer-backed part so TS/DOM typing is exact.
  const blob = new Blob([new Uint8Array(bytes)], { type: "application/zip" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

// ---- The view ---------------------------------------------------------------

export function SkillsView(props: {
  skills: Skill[];
  folders: SkillFolder[];
  manage: boolean;
  myUserId: string | null;
  ownerLabels: Record<string, string>;
  filters: { q: string; folder: string; presets: boolean };
  loadError: string | null;
}) {
  const { skills, folders, manage, myUserId, ownerLabels, filters, loadError } = props;
  const router = useRouter();
  const [query, setQuery] = React.useState(filters.q);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState<string | null>(null); // id of the in-flight row action
  const [uploadOpen, setUploadOpen] = React.useState(false);
  const [manageFoldersOpen, setManageFoldersOpen] = React.useState(false);

  const navigate = (next: Partial<{ q: string; folder: string; presets: boolean }>) => {
    const merged = { ...filters, ...next };
    const params = new URLSearchParams();
    if (merged.q) params.set("q", merged.q);
    if (merged.folder) params.set("folder", merged.folder);
    if (merged.presets) params.set("presets", "true");
    const qs = params.toString();
    router.push(`/skills${qs ? `?${qs}` : ""}`);
  };

  const run = async (key: string, fn: () => Promise<{ ok: boolean; message?: string }>) => {
    setBusy(key);
    setError(null);
    const res = await fn();
    if (!res.ok) setError(res.message ?? "Something went wrong.");
    setBusy(null);
    router.refresh();
  };

  const ownerLabel = (skill: Skill): string => {
    if (skill.is_seeded) return "Dropway";
    const id = skill.owner_id ?? "";
    if (id === myUserId) return "You";
    return ownerLabels[id] ?? "A teammate";
  };

  const downloadOne = async (skill: Skill) => {
    await run(`dl:${skill.id}`, async () => {
      const res = await downloadSkillAction(skill.id ?? "");
      if (!res.ok) return res;
      const { entries, skipped } = decodeSkillFiles(res.download, `${skill.slug}/`);
      saveZip(`${skill.slug}.zip`, entries);
      return skipped.length > 0
        ? { ok: false, message: `Downloaded, but skipped unsafe path(s): ${skipped.join(", ")}.` }
        : { ok: true };
    });
  };

  const downloadFolder = async (folder: SkillFolder) => {
    await run(`dlf:${folder.id}`, async () => {
      const res = await downloadSkillFolderAction(folder.id ?? "");
      if (!res.ok) return res;
      const entries: ZipEntry[] = [];
      const skipped: string[] = [];
      for (const s of res.download.skills ?? []) {
        if (s.truncated || !s.files) {
          if (s.slug) skipped.push(s.slug);
          continue;
        }
        const decoded = decodeSkillFiles(s, `${s.slug}/`);
        entries.push(...decoded.entries);
        skipped.push(...decoded.skipped);
      }
      if (entries.length === 0) {
        return { ok: false, message: "That folder has no downloadable skills yet." };
      }
      saveZip(`${folder.slug}-skills.zip`, entries);
      return skipped.length > 0
        ? { ok: false, message: `Downloaded, but some items were skipped: ${skipped.join(", ")}.` }
        : { ok: true };
    });
  };

  const activeFolder = folders.find((f) => f.slug === filters.folder) ?? null;

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Skills</h1>
          <p className="text-sm text-muted-foreground">
            Shared Claude skills for your whole org — upload yours, grab a teammate&apos;s, or
            start from the preset folders.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {manage ? (
            <Button variant="outline" onClick={() => setManageFoldersOpen(true)}>
              <FolderPlus className="mr-1.5 h-4 w-4" /> Manage folders
            </Button>
          ) : null}
          <Button variant="outline" asChild>
            <Link href="/skills/new">
              <Pencil className="mr-1.5 h-4 w-4" /> Write a skill
            </Link>
          </Button>
          <Button onClick={() => setUploadOpen(true)}>Upload skill</Button>
        </div>
      </div>

      {/* Search + folder filter row. */}
      <div className="flex flex-wrap items-center gap-2">
        <form
          className="relative"
          onSubmit={(e) => {
            e.preventDefault();
            navigate({ q: query });
          }}
        >
          <Search className="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search skills…"
            className="w-56 pl-8"
            aria-label="Search skills"
          />
        </form>
        <Button
          variant={!filters.folder && !filters.presets ? "secondary" : "ghost"}
          size="sm"
          onClick={() => navigate({ folder: "", presets: false })}
        >
          All
        </Button>
        {folders.map((f) => (
          <Button
            key={f.id}
            variant={filters.folder === f.slug ? "secondary" : "ghost"}
            size="sm"
            onClick={() => navigate({ folder: filters.folder === f.slug ? "" : (f.slug ?? "") })}
          >
            {f.title}
            <span className="ml-1.5 text-xs text-muted-foreground">{f.item_count ?? 0}</span>
          </Button>
        ))}
        <Button
          variant={filters.presets ? "secondary" : "ghost"}
          size="sm"
          onClick={() => navigate({ presets: !filters.presets })}
          title="Only admin-curated preset skills"
        >
          <Sparkles className="mr-1 h-3.5 w-3.5" /> Presets
        </Button>
        {activeFolder ? (
          <Button
            variant="outline"
            size="sm"
            disabled={busy === `dlf:${activeFolder.id}`}
            onClick={() => void downloadFolder(activeFolder)}
          >
            {busy === `dlf:${activeFolder.id}` ? (
              <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
            ) : (
              <Download className="mr-1.5 h-4 w-4" />
            )}
            Download all in {activeFolder.title}
          </Button>
        ) : null}
      </div>

      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      {loadError ? (
        <Card className="p-6 text-sm text-muted-foreground">Couldn&apos;t load skills: {loadError}</Card>
      ) : null}

      {/* The list. */}
      {skills.length === 0 && !loadError ? (
        <Card className="space-y-3 p-10 text-center text-sm text-muted-foreground">
          <p>No skills match. Write one in the browser, or upload a folder with a SKILL.md inside.</p>
          <div className="flex items-center justify-center gap-2">
            <Button variant="outline" size="sm" asChild>
              <Link href="/skills/new">
                <Pencil className="mr-1.5 h-4 w-4" /> Write a skill
              </Link>
            </Button>
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              Upload skill
            </Button>
          </div>
        </Card>
      ) : (
        <div className="space-y-3">
          {skills.map((skill) => {
            const mine = skill.owner_id === myUserId;
            const canEdit = manage || mine;
            return (
              <Card key={skill.id} className="flex flex-wrap items-center gap-3 p-4">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Link
                      href={`/skills/${skill.id}`}
                      className="font-medium hover:underline focus-visible:underline"
                    >
                      {skill.title || skill.slug}
                    </Link>
                    <code className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                      {skill.slug}
                    </code>
                    {(skill.folders ?? []).map((ref) => (
                      <Badge key={ref.id} variant={ref.is_preset ? "success" : "muted"}>
                        {ref.is_preset ? <Sparkles className="h-3 w-3" /> : null}
                        {ref.slug}
                      </Badge>
                    ))}
                  </div>
                  {skill.description ? (
                    <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{skill.description}</p>
                  ) : null}
                  <p className="mt-1 text-xs text-muted-foreground">
                    by {ownerLabel(skill)}
                    {skill.size_bytes ? <> · {formatBytes(skill.size_bytes)}</> : null}
                  </p>
                </div>
                <div className="flex items-center gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!skill.current_version_id || busy === `dl:${skill.id}`}
                    title={skill.current_version_id ? "Download as .zip" : "No content uploaded yet"}
                    onClick={() => void downloadOne(skill)}
                  >
                    {busy === `dl:${skill.id}` ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Download className="h-4 w-4" />
                    )}
                  </Button>
                  {canEdit ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy === `rm:${skill.id}`}
                      title="Delete skill"
                      onClick={() => {
                        if (!window.confirm(`Delete the skill "${skill.slug}" for the whole org?`)) return;
                        void run(`rm:${skill.id}`, () => deleteSkillAction(skill.id ?? ""));
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  ) : null}
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <SkillUploadDialog
        open={uploadOpen}
        onOpenChange={setUploadOpen}
        folders={folders}
        onDone={() => router.refresh()}
      />
      {manage ? (
        <FolderAdminDialog
          open={manageFoldersOpen}
          onOpenChange={setManageFoldersOpen}
          folders={folders}
          skills={skills}
          busy={busy}
          run={run}
        />
      ) : null}
    </div>
  );
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

// ---- Folder admin (create / rename / delete / curate presets) ---------------

function FolderAdminDialog(props: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  folders: SkillFolder[];
  skills: Skill[];
  busy: string | null;
  run: (key: string, fn: () => Promise<{ ok: boolean; message?: string }>) => Promise<void>;
}) {
  const { open, onOpenChange, folders, skills, busy, run } = props;
  const [newSlug, setNewSlug] = React.useState("");
  const [renaming, setRenaming] = React.useState<{ id: string; title: string } | null>(null);
  const [addTo, setAddTo] = React.useState<string>(""); // folder id an add-select is open for

  return (
    <Dialog open={open} onOpenChange={onOpenChange} className="max-w-2xl">
      <DialogHeader>
        <DialogTitle>Skill folders</DialogTitle>
      </DialogHeader>
      <DialogBody className="space-y-4">
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            const slug = newSlug.trim();
            if (!slug) return;
            setNewSlug("");
            void run("folder:create", () => createSkillFolderAction({ slug }));
          }}
        >
          <Input
            value={newSlug}
            onChange={(e) => setNewSlug(e.target.value)}
            placeholder="new-folder-slug"
            aria-label="New folder slug"
          />
          <Button type="submit" variant="outline" disabled={busy === "folder:create"}>
            <FolderPlus className="mr-1.5 h-4 w-4" /> Create
          </Button>
        </form>

        <div className="space-y-3">
          {folders.map((folder) => {
            const members = skills.filter((s) =>
              (s.folders ?? []).some((ref) => ref.id === folder.id),
            );
            const nonMembers = skills.filter(
              (s) => !(s.folders ?? []).some((ref) => ref.id === folder.id),
            );
            return (
              <Card key={folder.id} className="space-y-2 p-3">
                <div className="flex items-center gap-2">
                  {renaming?.id === folder.id ? (
                    <form
                      className="flex flex-1 items-center gap-2"
                      onSubmit={(e) => {
                        e.preventDefault();
                        const title = (renaming?.title ?? "").trim();
                        setRenaming(null);
                        if (!title) return;
                        void run(`folder:rename:${folder.id}`, () =>
                          renameSkillFolderAction({ folderId: folder.id ?? "", title }),
                        );
                      }}
                    >
                      <Input
                        autoFocus
                        value={renaming?.title ?? ""}
                        onChange={(e) => setRenaming({ id: folder.id ?? "", title: e.target.value })}
                        aria-label="Folder title"
                      />
                      <Button type="submit" size="sm" variant="outline">
                        Save
                      </Button>
                    </form>
                  ) : (
                    <>
                      <span className="font-medium">{folder.title}</span>
                      <code className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                        {folder.slug}
                      </code>
                      <span className="text-xs text-muted-foreground">
                        {folder.item_count ?? 0} skills
                      </span>
                      <span className="flex-1" />
                      <Button
                        variant="ghost"
                        size="sm"
                        title="Rename folder"
                        onClick={() => setRenaming({ id: folder.id ?? "", title: folder.title ?? "" })}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        title="Delete folder (skills survive)"
                        disabled={busy === `folder:rm:${folder.id}`}
                        onClick={() => {
                          if (!window.confirm(`Delete the folder "${folder.slug}"? Its skills are kept.`))
                            return;
                          void run(`folder:rm:${folder.id}`, () =>
                            deleteSkillFolderAction(folder.id ?? ""),
                          );
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </>
                  )}
                </div>

                {members.length > 0 ? (
                  <ul className="space-y-1">
                    {members.map((s) => {
                      const ref = (s.folders ?? []).find((r) => r.id === folder.id);
                      return (
                        <li key={s.id} className="flex items-center gap-2 text-sm">
                          <span className="min-w-0 flex-1 truncate">{s.title || s.slug}</span>
                          <Button
                            variant={ref?.is_preset ? "secondary" : "ghost"}
                            size="sm"
                            title={ref?.is_preset ? "Unmark as preset" : "Mark as preset (starter set)"}
                            onClick={() =>
                              void run(`preset:${folder.id}:${s.id}`, () =>
                                setPresetFlagAction({
                                  folderId: folder.id ?? "",
                                  skillId: s.id ?? "",
                                  isPreset: !ref?.is_preset,
                                }),
                              )
                            }
                          >
                            <Sparkles className="mr-1 h-3.5 w-3.5" />
                            {ref?.is_preset ? "Preset" : "Make preset"}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            title="Remove from folder"
                            onClick={() =>
                              void run(`unlink:${folder.id}:${s.id}`, () =>
                                removeSkillFromFolderAction({
                                  folderId: folder.id ?? "",
                                  skillId: s.id ?? "",
                                }),
                              )
                            }
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className="text-xs text-muted-foreground">No skills in this folder yet.</p>
                )}

                {nonMembers.length > 0 ? (
                  addTo === folder.id ? (
                    <select
                      className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                      aria-label={`Add a skill to ${folder.title}`}
                      defaultValue=""
                      onChange={(e) => {
                        const skillId = e.target.value;
                        setAddTo("");
                        if (!skillId) return;
                        void run(`link:${folder.id}:${skillId}`, () =>
                          addSkillToFolderAction({ folderId: folder.id ?? "", skillId }),
                        );
                      }}
                    >
                      <option value="" disabled>
                        Pick a skill to add…
                      </option>
                      {nonMembers.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.title || s.slug}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <Button variant="ghost" size="sm" onClick={() => setAddTo(folder.id ?? "")}>
                      + Add a skill
                    </Button>
                  )
                ) : null}
              </Card>
            );
          })}
        </div>
      </DialogBody>
      <DialogFooter>
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          Done
        </Button>
      </DialogFooter>
    </Dialog>
  );
}
