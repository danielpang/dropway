"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setChatPanelAction } from "@/app/(app)/chats/actions";
import { Switch } from "@/components/ui/switch";

/**
 * The served-panel toggle for a chat log (mirrors the site feed-visibility
 * toggle). On, the attached site serves the transcript to viewers as a "How
 * this was made" panel; off hides it at the edge without detaching the log.
 * Permitted for the log's creator or an org admin/owner; `disabled` reflects
 * that. Re-syncs to the authoritative value each PUT returns.
 */
export function ChatPanelToggle({
  chatId,
  initialEnabled,
  disabled,
  hasSite,
}: {
  chatId: string;
  initialEnabled: boolean;
  disabled: boolean;
  /** Whether the log is attached to a site (the toggle only matters then). */
  hasSite: boolean;
}) {
  const router = useRouter();

  const [enabled, setEnabled] = React.useState(initialEnabled);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setChatPanelAction({ chatId, enabled: next });
    if (result.ok) {
      const value = result.chatLog.panel_enabled ?? next;
      setEnabled(value);
      setNotice(
        value
          ? "Viewers of the site can read this conversation."
          : "The panel is hidden from the served site.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id={`chat-panel-label-${chatId}`} className="text-sm font-medium text-foreground">
            Show on the served site
          </p>
          <p id={`chat-panel-desc-${chatId}`} className="text-sm text-muted-foreground">
            {hasSite
              ? "When on, the attached site offers this conversation to viewers as a “How this was made” panel."
              : "Takes effect once this log is attached to a site."}
          </p>
        </div>
        <div className="pt-0.5">
          {pending ? (
            <span className="grid size-6 place-items-center text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
            </span>
          ) : (
            <Switch
              checked={enabled}
              onCheckedChange={onToggle}
              disabled={disabled || pending}
              aria-labelledby={`chat-panel-label-${chatId}`}
              aria-describedby={`chat-panel-desc-${chatId}`}
            />
          )}
        </div>
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
