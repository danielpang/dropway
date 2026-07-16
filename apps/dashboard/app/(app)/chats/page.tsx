import type { Metadata } from "next";
import Link from "next/link";
import { Globe, MessageSquareText, MonitorCheck, Plus } from "lucide-react";

import { sourceToolLabel } from "@/components/chats/source-tools";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { api, ApiError, type ChatLog, type Site } from "@/lib/api";

export const metadata: Metadata = { title: "Chats" };

// The chat library is cross-user, live org data; never serve a stale snapshot.
export const dynamic = "force-dynamic";

/**
 * The org chat library (server component): every shared AI conversation —
 * imported transcripts and agent-narrated sessions — with its source tool,
 * message count, attached site, and served-panel state. The org kill switch
 * (403) renders a disabled notice instead of the list.
 */
export default async function ChatsPage() {
  let chats: ChatLog[] = [];
  let disabled = false;
  let loadError: string | null = null;
  try {
    chats = await api.listChats();
  } catch (err) {
    if (err instanceof ApiError && err.status === 403) {
      disabled = true;
    } else {
      loadError =
        err instanceof ApiError
          ? `The API returned ${err.status}.`
          : "Couldn't reach the control-plane API.";
    }
  }

  // Slug lookup for the attached-site links. Best-effort: a failure only
  // downgrades the link label to the raw id.
  const sites: Site[] = disabled ? [] : await api.listSites().catch(() => []);
  const siteSlugs = new Map(sites.map((s) => [s.id ?? "", s.slug ?? ""]));

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Chats</h1>
          <p className="text-sm text-muted-foreground">
            Shared AI sessions — the conversations behind your team&rsquo;s sites.
          </p>
        </div>
        {!disabled && (
          <Button asChild>
            <Link href="/chats/new">
              <Plus aria-hidden />
              Share a conversation
            </Link>
          </Button>
        )}
      </div>

      {disabled ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Chat logs are disabled for this organization. An owner or admin can turn
          them back on in{" "}
          <Link href="/settings" className="font-medium text-foreground underline-offset-4 hover:underline">
            Settings
          </Link>
          .
        </Card>
      ) : loadError ? (
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Couldn&rsquo;t load the chat library. {loadError}
        </Card>
      ) : chats.length === 0 ? (
        <Card className="border-dashed p-10 text-center">
          <MessageSquareText
            className="mx-auto mb-3 size-8 text-muted-foreground"
            aria-hidden
          />
          <p className="text-sm font-medium text-foreground">No shared sessions yet</p>
          <p className="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
            Import a transcript from Claude Code, ChatGPT, or Cursor to show your
            team — or a site&rsquo;s visitors — how something was made.
          </p>
        </Card>
      ) : (
        <div className="space-y-3">
          {chats.map((chat) => (
            <ChatRow
              key={chat.id}
              chat={chat}
              siteSlug={chat.site_id ? siteSlugs.get(chat.site_id) : undefined}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ChatRow({ chat, siteSlug }: { chat: ChatLog; siteSlug?: string }) {
  const count = chat.message_count ?? 0;
  return (
    <Card className="p-4 transition-colors hover:border-foreground/20">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <Link
            href={`/chats/${chat.id}`}
            className="block truncate text-sm font-medium text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
          >
            {chat.title || "Untitled session"}
          </Link>
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="muted">{sourceToolLabel(chat.source_tool)}</Badge>
            <span>
              {count} {count === 1 ? "message" : "messages"}
            </span>
            {chat.created_at ? (
              <span>· {new Date(chat.created_at).toLocaleDateString()}</span>
            ) : null}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {chat.site_id ? (
            <>
              <Link
                href={`/sites/${chat.site_id}`}
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
              >
                <Globe className="size-3.5" aria-hidden />
                {siteSlug || "Attached site"}
              </Link>
              {chat.panel_enabled ? (
                <Badge variant="success">
                  <MonitorCheck className="size-3" aria-hidden />
                  On site
                </Badge>
              ) : (
                <Badge variant="muted">Panel off</Badge>
              )}
            </>
          ) : (
            <Badge variant="outline">Not attached</Badge>
          )}
        </div>
      </div>
    </Card>
  );
}
