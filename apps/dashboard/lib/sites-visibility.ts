import type { Site } from "@/lib/api";

/**
 * Who sees which site in the org-wide Sites listing, plus the byline label for
 * a site's owner.
 *
 * This is a DISCOVERY rule, not access control. GET /v1/sites is RLS-org-scoped
 * with no owner or feed_visible predicate, so the same rows are already returned
 * to every org member — and the CLI (`dropway sites list`) and the MCP
 * `list_sites` tool still list all of them. Filtering here decides what the page
 * *browses*, not what a member can reach: /sites/[id] stays resolvable by direct
 * link. Anything that needs to be a real boundary has to be enforced by the API.
 *
 * Kept free of `server-only` and of React so both helpers are unit-testable
 * without a renderer.
 */

/** The viewer, as far as the listing rule is concerned. */
export interface SiteViewer {
  /** The viewer's user id, or null when the org/session couldn't be resolved. */
  userId: string | null;
  /** True for org owners/admins — `canManage(myRole)` from lib/org.ts. */
  canManage: boolean;
}

/**
 * Whether `site` belongs in the viewer's Sites list: everything shared to the
 * org, plus the viewer's own sites, plus everything for owners/admins.
 *
 * `feed_visible !== false` (not `=== true`) is deliberate: every field on the
 * generated `Site` schema is optional, and the API defaults the flag to true, so
 * an absent flag must read as visible. The owner fallback requires `owner_id` to
 * be present so an undefined id can never match a null viewer.
 */
export function isSiteVisibleTo(
  site: Pick<Site, "owner_id" | "feed_visible">,
  viewer: SiteViewer,
): boolean {
  if (viewer.canManage) return true; // owners/admins keep a full inventory
  if (site.feed_visible !== false) return true;
  return Boolean(viewer.userId) && Boolean(site.owner_id) && site.owner_id === viewer.userId;
}

/**
 * The byline label for a site's owner: "You" for the viewer's own sites, else
 * the teammate's name (falling back to their email), else "A teammate" for an
 * owner who has left the org. Same ladder the feed uses for post authors.
 */
export function ownerLabel(
  ownerId: string | undefined,
  viewer: { userId: string | null },
  members: ReadonlyArray<{ userId: string; name: string | null; email: string }>,
): string {
  if (!ownerId) return "A teammate";
  if (viewer.userId && ownerId === viewer.userId) return "You";
  const member = members.find((m) => m.userId === ownerId);
  const label = member?.name ?? member?.email ?? "";
  return label.length > 0 ? label : "A teammate";
}
