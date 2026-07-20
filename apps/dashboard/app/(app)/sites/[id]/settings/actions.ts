"use server";

import { revalidatePath } from "next/cache";

import {
  api,
  ApiError,
  type AccessMode,
  type AllowlistEntry,
  type SetAccessResult,
} from "@/lib/api";
import { apiErrorMessage } from "@/lib/action-errors";

/** UI-level access selection. "unlisted" maps to mode=public + unlisted flag. */
export type AccessSelection =
  | "public"
  | "unlisted"
  | "password"
  | "allowlist"
  | "org_only";

export type SetAccessActionResult =
  | { ok: true; result: SetAccessResult }
  | { ok: false; message: string };

// Thin wrapper over the shared apiErrorMessage with this surface's access-specific
// 403/400 copy, so the mapping lives in one place (lib/action-errors).
function messageFor(err: ApiError, fallback: string): string {
  return apiErrorMessage(err, fallback, {
    403: "You don't have permission to change this site's access, or external sharing is disabled for your org.",
    400: "That access configuration is invalid.",
  });
}

/**
 * Update a site's access mode + policy (PUT /v1/sites/{id}/access). The Go API
 * is the authz boundary: it re-checks owner/admin, hashes any password
 * server-side, enforces the external-sharing policy, and rewrites the edge
 * RouteValue. "unlisted" is the public tier with the unlisted flag set.
 */
export async function setAccessAction(input: {
  siteId: string;
  selection: AccessSelection;
  password?: string;
  expiresAt?: string | null;
}): Promise<SetAccessActionResult> {
  const { siteId, selection } = input;

  const mode: AccessMode = selection === "unlisted" ? "public" : selection;
  const unlisted = selection === "unlisted";

  if (mode === "password" && !input.password) {
    return {
      ok: false,
      message: "Set a password for password-protected access.",
    };
  }

  // Normalize an empty expiry to "no expiry"; otherwise pass RFC3339.
  let expires_at: string | undefined;
  if (input.expiresAt) {
    const d = new Date(input.expiresAt);
    if (Number.isNaN(d.getTime())) {
      return { ok: false, message: "Enter a valid expiry date." };
    }
    if (d.getTime() <= Date.now()) {
      return { ok: false, message: "The expiry must be in the future." };
    }
    expires_at = d.toISOString();
  }

  try {
    const result = await api.setSiteAccess(siteId, {
      mode,
      unlisted,
      ...(mode === "password" && input.password
        ? { password: input.password }
        : {}),
      ...(expires_at ? { expires_at } : {}),
    });
    revalidatePath(`/sites/${siteId}/settings`);
    revalidatePath(`/sites/${siteId}`);
    revalidatePath("/dashboard");
    return { ok: true, result };
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: messageFor(err, "Could not update access. Try again."),
      };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type FeedVisibilityActionResult =
  | { ok: true; feedVisible: boolean }
  | { ok: false; message: string };

/**
 * Share a site to the org feed or make it private (PUT /v1/sites/{id}/feed). The
 * Go API authorizes this for the site's owner OR an org admin/owner. Feed
 * visibility is orthogonal to access mode — this changes nothing at the edge, only
 * whether the site shows up in teammates' feed.
 */
export async function setFeedVisibilityAction(input: {
  siteId: string;
  visible: boolean;
}): Promise<FeedVisibilityActionResult> {
  try {
    const res = await api.setSiteFeedVisibility(input.siteId, input.visible);
    revalidatePath(`/sites/${input.siteId}/settings`);
    revalidatePath(`/sites/${input.siteId}`);
    revalidatePath("/feed");
    revalidatePath("/dashboard");
    return { ok: true, feedVisible: res.feed_visible ?? input.visible };
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: messageFor(err, "Could not update feed sharing. Try again."),
      };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type FeedMetaActionResult =
  | { ok: true; title: string; description: string }
  | { ok: false; message: string };

/**
 * Set a site's feed title + description (PUT /v1/sites/{id}/feed-meta). The Go API
 * authorizes the site's owner or an org admin/owner. Empty strings clear a field.
 */
export async function setFeedMetaAction(input: {
  siteId: string;
  title: string;
  description: string;
}): Promise<FeedMetaActionResult> {
  const title = input.title.trim();
  const description = input.description.trim();
  if (title.length > 120) {
    return { ok: false, message: "Title must be at most 120 characters." };
  }
  if (description.length > 500) {
    return {
      ok: false,
      message: "Description must be at most 500 characters.",
    };
  }
  try {
    const res = await api.setSiteFeedMeta(input.siteId, { title, description });
    revalidatePath(`/sites/${input.siteId}/settings`);
    revalidatePath(`/sites/${input.siteId}`);
    revalidatePath("/feed");
    return {
      ok: true,
      title: res.title ?? title,
      description: res.description ?? description,
    };
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: messageFor(err, "Could not update feed details. Try again."),
      };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type AllowlistActionResult =
  | { ok: true; entry?: AllowlistEntry }
  | { ok: false; message: string };

/** Add an email to a site's allowlist (admin/owner; external grants gated). */
export async function addAllowlistAction(input: {
  siteId: string;
  email: string;
}): Promise<AllowlistActionResult> {
  const email = input.email.trim().toLowerCase();
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    return { ok: false, message: "Enter a valid email address." };
  }
  try {
    const entry = await api.addAllowlistEntry(input.siteId, email);
    revalidatePath(`/sites/${input.siteId}/settings`);
    return { ok: true, entry };
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: messageFor(err, "Could not add that email. Try again."),
      };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

/** Remove an email from a site's allowlist (admin/owner). */
export async function removeAllowlistAction(input: {
  siteId: string;
  email: string;
}): Promise<AllowlistActionResult> {
  try {
    await api.removeAllowlistEntry(input.siteId, input.email);
    revalidatePath(`/sites/${input.siteId}/settings`);
    return { ok: true };
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: messageFor(err, "Could not remove that email. Try again."),
      };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

/**
 * Flip the site's collaboration toggle (`allow_member_edits`) — whether
 * non-creators may modify its content (deploys/publish/previews). The Go API
 * re-checks creator-or-admin and 403s otherwise; deletion and access settings
 * are governed separately.
 */
export async function setSiteCollabAction(input: {
  id: string;
  allowMemberEdits: boolean;
}): Promise<
  { ok: true; allowMemberEdits: boolean } | { ok: false; message: string }
> {
  try {
    const site = await api.setSiteCollab(input.id, input.allowMemberEdits);
    revalidatePath(`/sites/${input.id}`);
    revalidatePath(`/sites/${input.id}/settings`);
    return {
      ok: true,
      allowMemberEdits: site.allow_member_edits ?? input.allowMemberEdits,
    };
  } catch (err) {
    return {
      ok: false,
      message: apiErrorMessage(
        err,
        "Could not update the collaboration setting.",
        {
          403: "Only the creator or an admin can change this.",
        },
      ),
    };
  }
}

export type DeleteSiteActionResult =
  | { ok: true }
  | { ok: false; message: string };

/**
 * Permanently delete a site (DELETE /v1/sites/{id}). The Go API re-checks that
 * the caller owns the site or is an org admin. On success the site (and every
 * version, route, and domain) is gone, so the client sends the user back to the
 * dashboard; we revalidate it so the deleted site drops out of the list.
 */
export async function deleteSiteAction(input: {
  siteId: string;
}): Promise<DeleteSiteActionResult> {
  try {
    await api.deleteSite(input.siteId);
  } catch (err) {
    if (err instanceof ApiError) {
      return {
        ok: false,
        message: apiErrorMessage(err, "Could not delete this site.", {
          403: "You don't have permission to delete this site. Only the site's owner or an org admin can.",
          404: "This site no longer exists.",
        }),
      };
    }
    throw err;
  }
  revalidatePath("/dashboard");
  return { ok: true };
}
