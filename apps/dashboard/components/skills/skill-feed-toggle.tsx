"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setSkillFeedVisibilityAction } from "@/app/(app)/skills/actions";
import { Switch } from "@/components/ui/switch";

/**
 * Per-skill org-feed sharing toggle (mirror of the site FeedVisibilityToggle). On
 * (default) shares the skill to the org feed the moment it's published — via UI,
 * MCP, or CLI — so teammates discover it; off makes it private (kept off the
 * feed). Permitted for the skill's owner or an org admin/owner; `disabled`
 * reflects that.
 */
export function SkillFeedToggle({
  skillId,
  initialVisible,
  disabled,
}: {
  skillId: string;
  initialVisible: boolean;
  disabled: boolean;
}) {
  const router = useRouter();
  const [visible, setVisible] = React.useState(initialVisible);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function onToggle(next: boolean) {
    setError(null);
    setPending(true);
    const result = await setSkillFeedVisibilityAction({ skillId, visible: next });
    if (result.ok) {
      setVisible(result.feedVisible);
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="flex items-start justify-between gap-4">
      <div className="space-y-1">
        <p id="skill-feed-label" className="text-sm font-medium text-foreground">
          Share to the org feed
        </p>
        <p id="skill-feed-desc" className="text-sm text-muted-foreground">
          When on, this skill appears in your organization&rsquo;s feed so teammates
          can discover, vote on, and discuss it. Turn it off to keep it private.
        </p>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </div>
      <div className="pt-0.5">
        {pending ? (
          <span className="grid size-6 place-items-center text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden />
          </span>
        ) : (
          <Switch
            checked={visible}
            onCheckedChange={onToggle}
            disabled={disabled || pending}
            aria-labelledby="skill-feed-label"
            aria-describedby="skill-feed-desc"
          />
        )}
      </div>
    </div>
  );
}
