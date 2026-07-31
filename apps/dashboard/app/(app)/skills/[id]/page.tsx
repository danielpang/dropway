import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, FileText, Pencil, Sparkles } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { setSkillCollabAction } from "@/app/(app)/skills/actions";
import { CollabToggle } from "@/components/collab-toggle";
import { api, ApiError, type Skill, type SkillDownload } from "@/lib/api";
import { canManage, loadActiveOrg } from "@/lib/org";
import { isSafeSkillPath } from "@/lib/skills-shared";
import { isUuid } from "@/lib/utils";
import { SkillDetailActions } from "@/components/skills/skill-detail-actions";
import { SkillFeedToggle } from "@/components/skills/skill-feed-toggle";

export const dynamic = "force-dynamic";

export async function generateMetadata(props: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await props.params;
  // Skip the API round-trip for non-id path segments (favicon/crawler probes).
  if (!isUuid(id)) return { title: "Skill" };
  try {
    const skill = await api.getSkill(id);
    return { title: `${skill.title || skill.slug} · Skills` };
  } catch {
    return { title: "Skill" };
  }
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

/**
 * Skill detail (server component): the read-only view of one shared skill — its
 * metadata, folder placements, and every file, with text files previewed inline
 * (SKILL.md first) so a teammate can inspect a skill before installing it.
 */
export default async function SkillDetailPage(props: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await props.params;

  // A skill id is always a UUID; a non-UUID segment is a stray probe (e.g. a
  // favicon fetched relative to this URL) — 404 before any authenticated read so
  // its 401 is never rethrown as a server error (noisy error tracking).
  if (!isUuid(id)) notFound();

  let skill: Skill;
  try {
    skill = await api.getSkill(id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  // Load file contents when the skill has an uploaded version (best-effort — the
  // metadata view still renders if the download fails).
  let download: SkillDownload | null = null;
  if (skill.current_version_id) {
    try {
      download = await api.downloadSkill(id);
    } catch {
      download = null;
    }
  }

  const org = await loadActiveOrg().catch(() => null);
  const manage = org ? canManage(org.myRole) : false;
  const mine = !!org?.myUserId && skill.owner_id === org.myUserId;
  const ownerLabel = skill.is_seeded
    ? "Dropway"
    : mine
      ? "You"
      : (org?.members ?? []).find((m) => m.userId === skill.owner_id)?.name ?? "A teammate";

  // SKILL.md first, then the rest alphabetically. Only safe paths are shown.
  const files = (download?.files ?? [])
    .filter((f) => f.path && isSafeSkillPath(f.path))
    .sort((a, b) => {
      if (a.path === "SKILL.md") return -1;
      if (b.path === "SKILL.md") return 1;
      return (a.path ?? "").localeCompare(b.path ?? "");
    });

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/skills"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Skills
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">{skill.title || skill.slug}</h1>
          <div className="flex flex-wrap items-center gap-2">
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
          <p className="text-xs text-muted-foreground">
            by {ownerLabel}
            {skill.size_bytes ? <> · {formatBytes(skill.size_bytes)}</> : null}
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          {(manage || mine) && skill.current_version_id ? (
            <Button variant="outline" size="sm" asChild>
              <Link href={`/skills/${skill.id}/edit`}>
                <Pencil className="mr-1.5 h-4 w-4" /> Edit
              </Link>
            </Button>
          ) : null}
          <SkillDetailActions
            skillId={skill.id ?? ""}
            slug={skill.slug ?? "skill"}
            canDownload={!!skill.current_version_id}
            canDelete={manage || mine}
          />
        </div>
      </div>

      {skill.description ? (
        <Card className="p-4 text-sm">{skill.description}</Card>
      ) : null}

      {!skill.current_version_id ? (
        <Card className="p-6 text-sm text-muted-foreground">
          This skill has no uploaded content yet.
        </Card>
      ) : files.length === 0 ? (
        <Card className="p-6 text-sm text-muted-foreground">
          Couldn&apos;t load this skill&apos;s files. Try downloading it instead.
        </Card>
      ) : (
        <div className="space-y-4">
          {files.map((f) => (
            <Card key={f.path} className="overflow-hidden">
              <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-3 py-2 text-sm">
                <FileText className="h-4 w-4 text-muted-foreground" />
                <span className="font-mono">{f.path}</span>
              </div>
              {f.encoding === "utf8" ? (
                <pre className="max-h-[32rem] overflow-auto p-3 text-xs leading-relaxed">
                  <code>{f.content}</code>
                </pre>
              ) : (
                <p className="p-3 text-sm text-muted-foreground">
                  Binary file — download the skill to view it.
                </p>
              )}
            </Card>
          ))}
        </div>
      )}

      {/* Feed sharing + collaboration live below the content — a skill
          auto-joins the feed on publish; the owner/admin can pull it off or
          restrict who may edit it here. */}
      {manage || mine ? (
        <Card className="space-y-4 p-4">
          <SkillFeedToggle
            skillId={skill.id ?? ""}
            initialVisible={skill.feed_visible ?? true}
            disabled={false}
          />
          <div className="border-t border-border pt-4">
            <CollabToggle
              resourceId={skill.id ?? ""}
              initialAllow={skill.allow_member_edits ?? true}
              disabled={false}
              action={setSkillCollabAction}
            />
          </div>
        </Card>
      ) : null}
    </div>
  );
}
