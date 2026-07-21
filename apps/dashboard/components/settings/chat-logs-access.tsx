"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setChatLogsEnabledAction } from "@/app/(app)/settings/actions";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";

/**
 * The org "Shared chat logs" kill switch (owner/admin only; mirrors the AI
 * builder toggle). Flips PATCH /v1/orgs/chat-logs. Every chat route re-checks
 * the flag, so turning it off blocks imports, appends, and the served "How
 * this was made" panels for the whole org immediately — existing logs are
 * kept, not deleted.
 */
export function ChatLogsAccess({
  initialEnabled,
  canManage,
}: {
  initialEnabled: boolean;
  canManage: boolean;
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
    const result = await setChatLogsEnabledAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.enabled);
      setNotice(
        result.enabled
          ? "Chat logs enabled for your team."
          : "Chat logs disabled. Sharing, appends, and served panels are blocked for this organization.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="space-y-4">
      {canManage ? (
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <p id="chat-logs-label" className="text-sm font-medium text-foreground">
              Allow shared chat logs
            </p>
            <p id="chat-logs-desc" className="text-sm text-muted-foreground">
              Members can share AI sessions to the chat library and their sites.
              Turning it off blocks it immediately; existing logs are kept.
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
                disabled={pending}
                aria-labelledby="chat-logs-label"
                aria-describedby="chat-logs-desc"
              />
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-center justify-between gap-3 text-sm">
          <span className="text-muted-foreground">
            Only owners and admins can change chat-log sharing.
          </span>
          <Badge variant={enabled ? "success" : "muted"} className="font-normal">
            {enabled ? "Enabled" : "Disabled"}
          </Badge>
        </div>
      )}

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
