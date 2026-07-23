"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setMemoryEnabledAction } from "@/app/(app)/settings/actions";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";

/**
 * The org "Company memory" toggle (owner/admin only). Flips memory_enabled via
 * PATCH /v1/orgs/memory. The flag is enforced on every /v1/ai/memories call, so
 * turning it off stops recall and new memories for the whole org immediately;
 * stored memories are kept, not deleted.
 */
export function MemoryAccess({
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
    const result = await setMemoryEnabledAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.memoryEnabled);
      setNotice(
        result.memoryEnabled
          ? "Company memory enabled for your team."
          : "Company memory disabled. Stored memories are kept but no longer used.",
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
            <p
              id="memory-label"
              className="text-sm font-medium text-foreground"
            >
              Allow company memory
            </p>
            <p id="memory-desc" className="text-sm text-muted-foreground">
              Dropway remembers durable facts about your organization and
              recalls them in future AI builds. Turning it off stops recall
              immediately; nothing is deleted.
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
                aria-labelledby="memory-label"
                aria-describedby="memory-desc"
              />
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-center justify-between gap-3 text-sm">
          <span className="text-muted-foreground">
            Only owners and admins can change company memory access.
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
