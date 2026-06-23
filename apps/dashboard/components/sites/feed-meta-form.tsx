"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import { setFeedMetaAction } from "@/app/(app)/sites/[id]/settings/actions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const TITLE_MAX = 120;
const DESCRIPTION_MAX = 500;

/**
 * Owner/admin form to set the human Title + Description a site shows in the org
 * feed. Both optional — clearing them falls the feed back to the site slug. Posts
 * to the Go API (PUT /v1/sites/{id}/feed-meta), which re-checks owner-or-admin.
 */
export function FeedMetaForm({
  siteId,
  initialTitle,
  initialDescription,
  disabled,
}: {
  siteId: string;
  initialTitle: string;
  initialDescription: string;
  disabled: boolean;
}) {
  const router = useRouter();

  const [title, setTitle] = React.useState(initialTitle);
  const [description, setDescription] = React.useState(initialDescription);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setFeedMetaAction({ siteId, title, description });
    if (result.ok) {
      setTitle(result.title);
      setDescription(result.description);
      setNotice("Feed details saved.");
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  return (
    <form onSubmit={onSubmit} className="space-y-4">
      <fieldset disabled={disabled} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="feed-title">Title</Label>
          <Input
            id="feed-title"
            name="feed-title"
            value={title}
            maxLength={TITLE_MAX}
            placeholder="A clear name for this site in the feed"
            onChange={(e) => setTitle(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">
            Shown in the org feed. Leave blank to use the site slug.
          </p>
        </div>

        <div className="space-y-2">
          <Label htmlFor="feed-description">Description</Label>
          <textarea
            id="feed-description"
            name="feed-description"
            value={description}
            maxLength={DESCRIPTION_MAX}
            rows={3}
            placeholder="What is this site? (optional)"
            onChange={(e) => setDescription(e.target.value)}
            className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
          />
          <p className="text-xs text-muted-foreground">
            {description.trim().length}/{DESCRIPTION_MAX}
          </p>
        </div>
      </fieldset>

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

      <Button type="submit" disabled={disabled || pending} aria-busy={pending}>
        {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
        Save feed details
      </Button>
    </form>
  );
}
