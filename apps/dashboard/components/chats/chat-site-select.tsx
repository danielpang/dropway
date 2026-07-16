"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setChatSiteAction } from "@/app/(app)/chats/actions";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";
import type { AttachableSite } from "@/components/chats/chat-import-form";

/**
 * Attach / detach / move a chat log's site binding. Each site holds at most
 * one log, so attaching to an occupied site fails with the API's 409 message
 * (detach the other log first). The save button only lights up when the
 * selection differs from the live binding.
 */
export function ChatSiteSelect({
  chatId,
  currentSiteId,
  sites,
  disabled,
}: {
  chatId: string;
  currentSiteId: string | null;
  sites: AttachableSite[];
  disabled: boolean;
}) {
  const router = useRouter();

  const [siteId, setSiteId] = React.useState(currentSiteId ?? "");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  const dirty = siteId !== (currentSiteId ?? "");

  async function onSave() {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setChatSiteAction({ chatId, siteId: siteId || null });
    if (result.ok) {
      const attached = result.chatLog.site_id ?? null;
      setSiteId(attached ?? "");
      setNotice(attached ? "Attached to the site." : "Detached — back in the library.");
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label htmlFor={`chat-site-${chatId}`}>Attached site</Label>
        <p className="text-sm text-muted-foreground">
          Attach this conversation to the site it produced. Each site holds one
          log.
        </p>
      </div>
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <Select
            id={`chat-site-${chatId}`}
            value={siteId}
            onChange={(e) => setSiteId(e.target.value)}
            disabled={disabled || pending}
          >
            <option value="">Not attached</option>
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.slug}
              </option>
            ))}
          </Select>
        </div>
        <Button
          type="button"
          variant="outline"
          onClick={() => void onSave()}
          disabled={disabled || pending || !dirty}
          aria-busy={pending}
        >
          {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
          Save
        </Button>
      </div>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
      {notice && (
        <p
          role="status"
          className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400"
        >
          {notice}
        </p>
      )}
    </div>
  );
}
