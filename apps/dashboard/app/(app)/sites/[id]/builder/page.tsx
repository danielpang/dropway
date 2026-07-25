import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";

import { publishVersionAction } from "@/app/(app)/sites/[id]/actions";
import { BuilderChat } from "@/components/ai/builder-chat";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";

export const dynamic = "force-dynamic";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const site = await api.getSite(id).catch(() => null);
  return { title: site?.slug ? `Build ${site.slug}` : "AI builder" };
}

/**
 * The AI builder workspace for a site: a chat that edits the site in a sandbox
 * and a live preview of the produced draft, with a publish button. The chat
 * itself is a client component that streams the turn through the SSE proxy.
 */
export default async function BuilderPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const site = await api.getSite(id).catch(() => null);
  if (!site) notFound();

  const catalog = await api.aiModels();

  // Resume the most recent session for this site (newest first) so the user sees
  // their prior conversation instead of a blank chat every visit. A brand-new
  // site has none, and the builder starts fresh (creating one on the first send).
  // The transcript is passed raw (role + OpenRouter message content): the chat
  // derives its rendering from it with the same transform it uses for reconnect
  // replays, so tool activity survives a reload too.
  const sessions = await api.aiSessions(id);
  const latest = sessions[0] ?? null;
  const initial = latest
    ? await api.aiSession(latest.id)
    : { session: null, messages: [], draft: null };
  const initialTranscript = initial.messages.map((m) => ({
    seq: m.seq,
    role: m.role,
    content: m.content,
  }));
  const initialDraft = initial.draft
    ? {
        versionId: initial.draft.version_id,
        previewUrl: initial.draft.preview_url,
        expiresAt: initial.draft.expires_at ?? "",
        accessMode: initial.draft.access_mode,
      }
    : null;

  async function publish(versionId: string) {
    "use server";
    const result = await publishVersionAction({ siteId: id, versionId });
    return result.ok
      ? { ok: true as const }
      : { ok: false as const, message: result.message };
  }

  return (
    <div className="space-y-4 p-4 md:p-6">
      <div className="flex items-center gap-3">
        <Button asChild variant="ghost" size="icon">
          <Link href={`/sites/${id}`}>
            <ArrowLeft className="h-4 w-4" />
          </Link>
        </Button>
        <div>
          <h1 className="text-lg font-semibold">Build with AI</h1>
          <p className="text-sm text-muted-foreground">
            Editing {site.slug}. Review the preview, then publish when you are
            happy.
          </p>
        </div>
      </div>

      {catalog.models.length === 0 ? (
        <div className="rounded-lg border bg-card p-6 text-sm text-muted-foreground">
          The AI builder is not available on this deployment yet.
        </div>
      ) : (
        <BuilderChat
          siteId={id}
          initialModel={latest?.model || catalog.default || (catalog.models[0]?.id ?? "")}
          models={catalog.models}
          onPublish={publish}
          initialSessionId={latest?.id ?? null}
          initialTranscript={initialTranscript}
          initialDraft={initialDraft}
          initialStatus={initial.session?.status ?? latest?.status ?? null}
        />
      )}
    </div>
  );
}
