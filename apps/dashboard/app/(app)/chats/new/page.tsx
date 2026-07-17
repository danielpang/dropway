import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { ChatImportForm, type AttachableSite } from "@/components/chats/chat-import-form";
import { api } from "@/lib/api";

export const metadata: Metadata = { title: "Share a conversation · Chats" };

// Loads the org's sites fresh so the attach picker is current.
export const dynamic = "force-dynamic";

/**
 * The share-a-conversation page (server component): loads the org's sites for
 * the optional attach picker, then hands off to the client import form (paste
 * or upload a transcript → POST /v1/chats via a server action).
 */
export default async function NewChatPage() {
  const sites: AttachableSite[] = await api
    .listSites()
    .then((all) =>
      all
        .filter((s) => s.id && s.slug)
        .map((s) => ({ id: s.id ?? "", slug: s.slug ?? "" })),
    )
    .catch(() => []);

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <Link
        href="/chats"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Chats
      </Link>

      <div className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Share a conversation</h1>
        <p className="text-sm text-muted-foreground">
          Import a session from Claude Code, ChatGPT, or Cursor so your team can
          read how something was made — and optionally show it on the site it
          built.
        </p>
      </div>

      <ChatImportForm sites={sites} />
    </div>
  );
}
