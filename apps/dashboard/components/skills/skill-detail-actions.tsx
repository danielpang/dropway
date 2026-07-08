"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Download, Loader2, Trash2 } from "lucide-react";

import { deleteSkillAction, downloadSkillAction } from "@/app/(app)/skills/actions";
import { Button } from "@/components/ui/button";
import { isSafeSkillPath } from "@/lib/skills-shared";
import { buildZip, type ZipEntry } from "@/lib/zip";

/** Download + delete controls for the skill detail page (client-side zip save). */
export function SkillDetailActions(props: {
  skillId: string;
  slug: string;
  canDownload: boolean;
  canDelete: boolean;
}) {
  const { skillId, slug, canDownload, canDelete } = props;
  const router = useRouter();
  const [busy, setBusy] = React.useState<"download" | "delete" | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  const download = async () => {
    setBusy("download");
    setError(null);
    const res = await downloadSkillAction(skillId);
    if (!res.ok) {
      setError(res.message);
      setBusy(null);
      return;
    }
    const entries: ZipEntry[] = [];
    for (const f of res.download.files ?? []) {
      if (!f.path || !isSafeSkillPath(f.path)) continue;
      const data =
        f.encoding === "base64"
          ? Uint8Array.from(atob(f.content ?? ""), (c) => c.charCodeAt(0))
          : new TextEncoder().encode(f.content ?? "");
      entries.push({ path: `${slug}/${f.path}`, data });
    }
    const blob = new Blob([new Uint8Array(buildZip(entries))], { type: "application/zip" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${slug}.zip`;
    a.click();
    URL.revokeObjectURL(url);
    setBusy(null);
  };

  const remove = async () => {
    if (!window.confirm(`Delete the skill "${slug}" for the whole org?`)) return;
    setBusy("delete");
    setError(null);
    const res = await deleteSkillAction(skillId);
    if (!res.ok) {
      setError(res.message);
      setBusy(null);
      return;
    }
    router.push("/skills");
    router.refresh();
  };

  return (
    <div className="flex flex-col items-end gap-1.5">
      <div className="flex items-center gap-1.5">
        <Button variant="outline" size="sm" disabled={!canDownload || busy !== null} onClick={() => void download()}>
          {busy === "download" ? (
            <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
          ) : (
            <Download className="mr-1.5 h-4 w-4" />
          )}
          Download
        </Button>
        {canDelete ? (
          <Button variant="ghost" size="sm" disabled={busy !== null} onClick={() => void remove()} title="Delete skill">
            <Trash2 className="h-4 w-4" />
          </Button>
        ) : null}
      </div>
      {error ? (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}
