import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, Globe } from "lucide-react";

import { ChatDeleteButton } from "@/components/chats/chat-delete-button";
import { ChatPanelToggle } from "@/components/chats/chat-panel-toggle";
import { ChatSiteSelect } from "@/components/chats/chat-site-select";
import type { AttachableSite } from "@/components/chats/chat-import-form";
import { sourceToolLabel } from "@/components/chats/source-tools";
import { TranscriptView } from "@/components/chats/transcript-view";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, ApiError, type ChatLog, type ChatMessage, type Site } from "@/lib/api";
import { canManage as roleCanManage, loadActiveOrg } from "@/lib/org";

export const dynamic = "force-dynamic";

export async function generateMetadata(props: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await props.params;
  try {
    const chat = await api.getChat(id);
    return { title: `${chat.title || "Untitled session"} · Chats` };
  } catch {
    return { title: "Chat" };
  }
}

/**
 * Chat detail (server component): the shared transcript plus its management
 * controls — attach/detach/move the site binding, the served-panel toggle, and
 * delete (log or single message). Mutations are creator-or-admin; everyone in
 * the org can read.
 */
export default async function ChatDetailPage(props: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await props.params;

  let chat: ChatLog;
  try {
    chat = await api.getChat(id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }

  // Messages + sites + org role in parallel; the page already has the log.
  const [messages, sites, org] = await Promise.all([
    api.listChatMessages(id).catch((): ChatMessage[] => []),
    api.listSites().catch((): Site[] => []),
    loadActiveOrg().catch(() => null),
  ]);

  const isAdmin = org ? roleCanManage(org.myRole) : false;
  const mine = !!org?.myUserId && chat.created_by === org.myUserId;
  const manage = isAdmin || mine;

  const creatorLabel = mine
    ? "You"
    : (org?.members ?? []).find((m) => m.userId === chat.created_by)?.name ??
      "A teammate";

  const attachable: AttachableSite[] = sites
    .filter((s) => s.id && s.slug)
    .map((s) => ({ id: s.id ?? "", slug: s.slug ?? "" }));
  const attachedSlug = chat.site_id
    ? sites.find((s) => s.id === chat.site_id)?.slug
    : undefined;

  const count = chat.message_count ?? messages.length;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/chats"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Chats
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">
            {chat.title || "Untitled session"}
          </h1>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="muted">{sourceToolLabel(chat.source_tool)}</Badge>
            {chat.site_id ? (
              <Link
                href={`/sites/${chat.site_id}`}
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
              >
                <Globe className="size-3.5" aria-hidden />
                {attachedSlug || "Attached site"}
              </Link>
            ) : null}
          </div>
          <p className="text-xs text-muted-foreground">
            by {creatorLabel} · {count} {count === 1 ? "message" : "messages"}
            {chat.created_at ? (
              <> · {new Date(chat.created_at).toLocaleString()}</>
            ) : null}
          </p>
        </div>
        {manage ? (
          <ChatDeleteButton
            chatId={chat.id ?? id}
            title={chat.title || "Untitled session"}
          />
        ) : null}
      </div>

      {/* Transcript */}
      <Card className="p-4 sm:p-6">
        <TranscriptView
          chatId={chat.id ?? id}
          messages={messages}
          canManage={manage}
        />
      </Card>

      {/* Sharing: where (and whether) the served site shows this conversation. */}
      {manage ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Sharing</CardTitle>
          </CardHeader>
          <CardContent className="space-y-6">
            <ChatSiteSelect
              chatId={chat.id ?? id}
              currentSiteId={chat.site_id ?? null}
              sites={attachable}
              disabled={false}
            />
            <div className="border-t border-border pt-4">
              <ChatPanelToggle
                chatId={chat.id ?? id}
                initialEnabled={chat.panel_enabled ?? false}
                disabled={false}
                hasSite={!!chat.site_id}
              />
            </div>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
