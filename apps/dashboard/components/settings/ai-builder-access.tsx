"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setAiBuilderEnabledAction } from "@/app/(app)/settings/actions";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";

/**
 * The org "AI website builder" toggle (owner/admin only). Flips
 * org_meta.ai_enabled via PATCH /v1/orgs/ai. The AI builder is enforced on every
 * /v1/ai/* call, so turning it off stops new builder sessions for the whole org
 * immediately. On by default; shown only for paid plans (the builder requires a
 * paid plan, so a free org has nothing to toggle).
 */
export function AiBuilderAccess({
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
    const result = await setAiBuilderEnabledAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.aiEnabled);
      setNotice(
        result.aiEnabled
          ? "AI builder enabled for your team."
          : "AI builder disabled. New builder sessions are blocked for this organization.",
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
            <p id="ai-label" className="text-sm font-medium text-foreground">
              Allow the AI builder
            </p>
            <p id="ai-desc" className="text-sm text-muted-foreground">
              When on, members can create and edit sites by chatting with AI.
              Turning it off blocks new builder sessions for the organization
              right away.
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
                aria-labelledby="ai-label"
                aria-describedby="ai-desc"
              />
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-center justify-between gap-3 text-sm">
          <span className="text-muted-foreground">
            Only owners and admins can change AI builder access.
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
