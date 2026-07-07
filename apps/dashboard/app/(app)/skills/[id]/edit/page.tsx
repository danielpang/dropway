import type { Metadata } from "next";
import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { ArrowLeft } from "lucide-react";

import { SkillEditor, type SkillEditState } from "@/components/skills/skill-editor";
import { api, ApiError, type Skill, type SkillDownload, type SkillFolder } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";
import type { AuthoredFile } from "@/lib/skill-authoring";

export const metadata: Metadata = { title: "Edit skill · Skills" };

// Loads the skill's current content fresh; never serve a stale editor.
export const dynamic = "force-dynamic";

/**
 * Edit a skill (server component): only the skill's owner or an org admin may
 * edit. Loads the current SKILL.md + files into the in-browser editor; saving
 * uploads a new version. Binary files are carried through unchanged.
 */
export default async function EditSkillPage(props: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await props.params;

  let skill: Skill;
  try {
    skill = await api.getSkill(id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  const org = await loadActiveOrg().catch(() => null);
  const manage = org ? canManage(org.myRole) : false;
  const mine = !!org?.myUserId && skill.owner_id === org.myUserId;
  // Only the owner or an admin may edit — everyone else is bounced to the read view.
  if (!manage && !mine) redirect(`/skills/${id}`);
  // Nothing to edit until content exists (author from scratch on /skills/new).
  if (!skill.current_version_id) redirect(`/skills/${id}`);

  let download: SkillDownload;
  try {
    download = await api.downloadSkill(id);
  } catch {
    // Content couldn't be loaded — send them back rather than an empty editor.
    redirect(`/skills/${id}`);
  }

  let folders: SkillFolder[] = [];
  try {
    folders = await api.listSkillFolders();
  } catch {
    folders = [];
  }

  // Split the downloaded files: SKILL.md → the Markdown editor, other text files
  // → editable rows, binary files → carried through unchanged.
  let skillMd = "";
  const extras: AuthoredFile[] = [];
  const binary: { path: string; base64: string }[] = [];
  for (const f of download.files ?? []) {
    if (!f.path) continue;
    if (f.path === "SKILL.md") {
      skillMd = f.content ?? "";
    } else if (f.encoding === "base64") {
      binary.push({ path: f.path, base64: f.content ?? "" });
    } else {
      extras.push({ path: f.path, content: f.content ?? "" });
    }
  }
  extras.sort((a, b) => a.path.localeCompare(b.path));

  const edit: SkillEditState = {
    skillId: skill.id ?? id,
    slug: skill.slug ?? "",
    selectedFolderIds: (skill.folders ?? []).map((r) => r.id ?? "").filter(Boolean),
    skillMd,
    extras,
    binary,
  };

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href={`/skills/${id}`}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> {skill.title || skill.slug}
      </Link>

      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Edit skill</h1>
        <p className="text-sm text-muted-foreground">
          Update the SKILL.md and supporting files. Saving publishes a new version.
        </p>
      </div>

      <SkillEditor folders={folders} edit={edit} />
    </div>
  );
}
