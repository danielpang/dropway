import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { SkillEditor } from "@/components/skills/skill-editor";
import { api, type SkillFolder } from "@/lib/api";

export const metadata: Metadata = { title: "Write a skill · Skills" };

// Loads the org's folders fresh so the folder chips are current.
export const dynamic = "force-dynamic";

/**
 * The write-a-skill page (server component): loads the org's folders, then hands
 * off to the in-browser Markdown editor. Authoring reuses the same upload
 * pipeline as drag-and-drop — the editor composes SKILL.md + extra files into
 * the create → prepare → upload → finalize flow.
 */
export default async function NewSkillPage() {
  let folders: SkillFolder[] = [];
  try {
    folders = await api.listSkillFolders();
  } catch {
    folders = [];
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/skills"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Skills
      </Link>

      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Write a skill</h1>
        <p className="text-sm text-muted-foreground">
          Author a skill in Markdown and share it with your org. A skill is a
          <code className="mx-1 rounded bg-muted px-1 py-0.5 text-xs">SKILL.md</code>
          plus any supporting files.
        </p>
      </div>

      <SkillEditor folders={folders} />
    </div>
  );
}
