"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setFeedVisibilityAction } from "@/app/(app)/sites/[id]/settings/actions";
import { Switch } from "@/components/ui/switch";

/**
 * Per-site org-feed sharing toggle. On (default) shares the site to the org feed
 * — the cross-user discovery surface — so teammates see it the moment it's created
 * or published; off makes the site private (kept off the feed). This is
 * orthogonal to the access mode: a private site keeps serving under whatever
 * access it has, it's just hidden from the feed listing.
 *
 * Renders the site's LIVE feed_visible value (passed from the server-rendered
 * settings page) and re-syncs to the authoritative value each PUT returns.
 * Permitted for the site's owner or an org admin/owner; `disabled` reflects that.
 */
export function FeedVisibilityToggle({
  siteId,
  initialVisible,
  disabled,
}: {
  siteId: string;
  initialVisible: boolean;
  disabled: boolean;
}) {
  const router = useRouter();

  const [visible, setVisible] = React.useState(initialVisible);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setFeedVisibilityAction({ siteId, visible: next });
    if (result.ok) {
      setVisible(result.feedVisible);
      setNotice(
        result.feedVisible
          ? "Shared to the org feed."
          : "Hidden from the org feed. This site is private.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id="feed-vis-label" className="text-sm font-medium text-foreground">
            Share to the org feed
          </p>
          <p id="feed-vis-desc" className="text-sm text-muted-foreground">
            When on, this site appears in your organization&rsquo;s feed so
            teammates can discover it. Turn it off to keep the site private. It
            stays out of the feed, but its access settings are unchanged.
          </p>
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
              aria-labelledby="feed-vis-label"
              aria-describedby="feed-vis-desc"
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
