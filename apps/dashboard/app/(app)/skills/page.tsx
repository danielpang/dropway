import type { Metadata } from "next";

import { SkillsView } from "@/components/skills/skills-view";
import { api, ApiError, type Skill, type SkillFolder } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Skills" };

// Skills are cross-user, live org data; never serve a stale snapshot.
export const dynamic = "force-dynamic";

/**
 * The org skills library (server component): every skill teammates have shared,
 * searchable and filterable by admin-curated folder (engineering / product /
 * marketing by default), with drag-and-drop upload and one-click download.
 * Admins additionally curate folders + the preset starter set. The first visit
 * lazily seeds the org's default folders + preset skills server-side.
 */
export default async function SkillsPage(props: {
  searchParams: Promise<{ q?: string; folder?: string; presets?: string }>;
}) {
  const searchParams = await props.searchParams;
  const q = searchParams.q ?? "";
  const folder = searchParams.folder ?? "";
  const presets = searchParams.presets === "true";

  const [skillsResult, foldersResult, orgResult] = await Promise.allSettled([
    api.listSkills({ q: q || undefined, folder: folder || undefined, presets }),
    api.listSkillFolders(),
    loadActiveOrg(),
  ]);

  let skills: Skill[] = [];
  let folders: SkillFolder[] = [];
  let loadError: string | null = null;
  if (skillsResult.status === "fulfilled") {
    skills = skillsResult.value;
  } else {
    const err = skillsResult.reason;
    loadError =
      err instanceof ApiError
        ? `The API returned ${err.status}.`
        : "Couldn't reach the control-plane API.";
  }
  if (foldersResult.status === "fulfilled") folders = foldersResult.value;

  const org = orgResult.status === "fulfilled" ? orgResult.value : null;
  const myUserId = org?.myUserId ?? null;
  const manage = org ? canManage(org.myRole) : false;

  // Owner labels for the list (seeded presets render as "Dropway").
  const ownerLabels: Record<string, string> = {};
  for (const m of org?.members ?? []) {
    ownerLabels[m.userId] = m.name ?? m.email ?? "A teammate";
  }

  return (
    <SkillsView
      skills={skills}
      folders={folders}
      manage={manage}
      myUserId={myUserId}
      ownerLabels={ownerLabels}
      filters={{ q, folder, presets }}
      loadError={loadError}
    />
  );
}
