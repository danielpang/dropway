"use server";

import { revalidatePath } from "next/cache";
import { headers } from "next/headers";

import { api, ApiError } from "@/lib/api";
import { auth } from "@/lib/auth";
import { betterAuthUrl } from "@/lib/env";
import { mfaResetEmail } from "@/lib/email-templates";
import { resetUserTwoFactor } from "@/lib/mfa-server";
import { canManage, loadActiveOrg } from "@/lib/org";

/**
 * Result of a hard-revocation ("sign out / revoke access everywhere") write.
 * `unavailable` is the graceful path for builds where the Go API's Phase-4
 * /v1/orgs/revoke-access endpoint isn't present yet (404), the UI shows an
 * "not available on this deployment" note instead of an error.
 */
export type RevokeActionResult =
  | { ok: true; minIat: number | null }
  | { ok: false; unavailable: true }
  | { ok: false; unavailable?: false; message: string };

/**
 * Hard-revoke edge tokens for a subject via the KV denylist (the REVOCATION
 * DENYLIST CONTRACT): bumps `revoked:<kind>:<id>.min_iat` so every edge token
 * issued before now is rejected at the serving Worker and the /authz exchange.
 *
 *   - kind="org"  → org-wide kill switch: signs every viewer out of every gated
 *     site in the org, forcing re-auth. Used for incident response / tightening.
 *   - kind="user" → revoke one member's content access immediately (pairs with
 *     removing them, so they can't ride a still-valid 15m edge token).
 *
 * The Go API re-checks owner/admin (this is not the security boundary) and the
 * write is idempotent server-side (max of existing/new min_iat). A stale
 * denylist only fails CLOSED (extra re-auth), never opens access.
 */
export async function revokeAccessAction(input: {
  kind: "org" | "user";
  id: string;
}): Promise<RevokeActionResult> {
  if (!input.id) {
    return { ok: false, message: "Nothing to revoke." };
  }
  try {
    const result = await api.revokeAccess(input);
    // Membership/access changed → refresh the members + audit views.
    revalidatePath("/members");
    revalidatePath("/audit");
    return { ok: true, minIat: result.min_iat ?? null };
  } catch (err) {
    if (err instanceof ApiError) {
      // 404 → endpoint not on this build (Phase-4 revocation not wired yet).
      if (err.status === 404) return { ok: false, unavailable: true };
      const apiMsg = (err.body as { message?: string } | null)?.message;
      if (apiMsg) return { ok: false, message: apiMsg };
      if (err.status === 403) {
        return {
          ok: false,
          message: "Only owners and admins can revoke access.",
        };
      }
      return { ok: false, message: "Could not revoke access. Try again." };
    }
    return { ok: false, message: "Could not reach the API. Try again." };
  }
}

export type MembersPreflightResult =
  | { ok: true }
  | { ok: false; atCap: boolean; message: string; upgradeUrl?: string };

/**
 * Members-cap preflight for the invite flow (H8). Asks the Go API whether the org
 * may add another member (members + pending invitations vs the plan cap); the cap
 * decision lives in the Go API so the cloud caps never ship in this build. A 402
 * resolves to atCap=true with an upgrade message + URL so the form can block the
 * invite and prompt an upgrade, instead of a generic Better Auth error after the
 * fact.
 */
export async function preflightMembersAction(): Promise<MembersPreflightResult> {
  try {
    await api.preflightMembers();
    return { ok: true };
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 402) {
        const body = err.body as {
          next_tier?: string;
          upgrade_url?: string;
          sales_url?: string;
          max?: number;
        } | null;
        const upgradeUrl = body?.upgrade_url ?? body?.sales_url;
        const max = body?.max ? ` (${body.max})` : "";
        const message =
          body?.next_tier === "contact_sales"
            ? `Your organization is at its member limit${max}. Contact sales to add more members.`
            : `Your organization is at its member limit${max}. Upgrade${
                body?.next_tier ? ` to ${body.next_tier}` : ""
              } to invite more members.`;
        return { ok: false, atCap: true, message, upgradeUrl };
      }
      const apiMsg = (err.body as { message?: string } | null)?.message;
      return { ok: false, atCap: false, message: apiMsg ?? "Could not check member limits. Try again." };
    }
    return { ok: false, atCap: false, message: "Could not reach the API. Try again." };
  }
}

/**
 * Record a `member.invite` audit entry after Better Auth creates an org invitation.
 * Best-effort telemetry to the audit trail: the invitation already exists and is
 * authoritative, so a failure here (or an older API build without the endpoint) is
 * swallowed — it must never turn a successful invite into a UI error. Called by the
 * invite form right after `organization.inviteMember` succeeds.
 */
export async function recordInviteSentAction(input: {
  email: string;
  role: string;
}): Promise<void> {
  try {
    await api.recordMemberInvite(input);
    // The new event should show on the audit page.
    revalidatePath("/audit");
  } catch {
    // Audit recording is best-effort; never surface it to the inviter.
  }
}

/**
 * Record a `member.join` audit entry after the caller accepts an invitation. MUST
 * run after the joined org is set active (the form awaits setActive first), so the
 * Go API scopes the row to the org just joined. Best-effort like the invite path:
 * the membership already exists, so any failure is swallowed.
 */
export async function recordMemberJoinAction(): Promise<void> {
  try {
    await api.recordMemberJoin();
  } catch {
    // Best-effort; the join already succeeded regardless of the trail write.
  }
}

/**
 * Remove a member from the active org AND hard-revoke their access (C2), in one
 * authorized server action. This is the ONLY entry point for the privileged
 * session-kill below — it is deliberately the place authorization is enforced.
 *
 * Authorization is delegated to Better Auth's own `removeMember` endpoint, which
 * requires the CALLER to be an owner/admin of the active org (the `member:delete`
 * permission) and the TARGET member to belong to that org, rejecting otherwise.
 * Crucially, we then revoke ONLY the userId Better Auth confirms it removed — so
 * this can never be coerced into killing an arbitrary user's sessions across
 * tenants (the previous version took a raw client-supplied userId with no check).
 *
 * After the member row is deleted, removal MUST also:
 *   1. revoke the removed user's Better Auth sessions, so the jwt() plugin can't
 *      re-mint a fresh JWT they'd use to re-authorize at /authz; and
 *   2. bump the edge denylist (revoked:user:<id>), so every edge token they hold is
 *      rejected immediately at the serving Worker instead of riding the 15m TTL.
 *
 * Without this, a removed member kept a valid ~10m JWT + live edge tokens and could
 * keep viewing gated sites (and, paired with the /authz mint denylist check, re-mint
 * them) for minutes after removal. Called by the member list; takes the `member`
 * row id (the same handle the role/remove UI already uses).
 */
export type RemoveMemberResult =
  // The member row was deleted; `revoke` reports whether the follow-up
  // session/edge revocation also succeeded (the row is gone either way).
  | { removed: true; revoke: RevokeActionResult }
  // The removal itself was refused (e.g. unauthorized caller, not a member of
  // this org) — nothing changed.
  | { removed: false; message: string };

export async function removeMemberAction(input: {
  memberId: string;
}): Promise<RemoveMemberResult> {
  if (!input.memberId) {
    return { removed: false, message: "Missing member." };
  }

  // Remove the member through Better Auth, which is the authorization boundary:
  // it verifies the caller is an owner/admin of the active org and the target is
  // a member of it, and returns the member it actually removed. organizationId is
  // omitted so it binds to the caller's active org (the members page's scope).
  let removedUserId: string;
  try {
    const res = (await auth.api.removeMember({
      body: { memberIdOrEmail: input.memberId },
      headers: await headers(),
    })) as { member?: { userId?: string } } | null;
    const uid = res?.member?.userId;
    if (!uid) {
      return { removed: false, message: "Could not remove the member. Try again." };
    }
    removedUserId = uid;
  } catch (err) {
    // Better Auth throws an APIError on an unauthorized caller or a target that
    // isn't in the org — surface its message (e.g. "you are not allowed…").
    const msg = err as { body?: { message?: string }; message?: string } | null;
    return {
      removed: false,
      message: msg?.body?.message ?? msg?.message ?? "Could not remove the member.",
    };
  }

  return { removed: true, revoke: await finalizeMemberRevocation(removedUserId) };
}

export type ResetMemberMfaResult =
  | { ok: true }
  | { ok: false; message: string };

/**
 * Clear a member's two-factor enrollment (the LOCKOUT RECOVERY path: they lost
 * their authenticator and their backup codes). Deletes their TOTP secret +
 * backup codes, flips twoFactorEnabled off, and kills their sessions so they
 * re-enroll on a fresh sign-in — under org enforcement they land directly in
 * mandatory setup.
 *
 * Authorization mirrors the member-list edit rules and happens HERE (this
 * action is the only entry point to the privileged reset): the caller must be
 * an owner/admin of the active org, the target must be a member of that org,
 * never the caller themselves (self-service lives on /account/security), and
 * only an owner may reset an owner. The target's user id is resolved from the
 * caller's own org membership list — never trusted from the client.
 */
export async function resetMemberMfaAction(input: {
  memberId: string;
}): Promise<ResetMemberMfaResult> {
  if (!input.memberId) return { ok: false, message: "Missing member." };

  const org = await loadActiveOrg();
  if (!org || !canManage(org.myRole)) {
    return {
      ok: false,
      message: "Only owners and admins can reset a member's two-factor.",
    };
  }
  const target = org.members.find((m) => m.id === input.memberId);
  if (!target) {
    return { ok: false, message: "That person is not a member of this organization." };
  }
  if (org.myUserId !== null && target.userId === org.myUserId) {
    return {
      ok: false,
      message: "Manage your own two-factor from Account security instead.",
    };
  }
  if (target.role === "owner" && org.myRole !== "owner") {
    return { ok: false, message: "Only an owner can reset an owner's two-factor." };
  }

  try {
    await resetUserTwoFactor(target.userId);
  } catch {
    return { ok: false, message: "Could not reset two-factor. Try again." };
  }

  // Trail + tripwire, both best-effort: the reset already happened.
  try {
    await api.recordMfaReset({ user_id: target.userId });
  } catch {
    // Audit trail is best-effort.
  }
  if (target.email) {
    try {
      const { sendEmail } = await import("@/lib/email");
      const { subject, html, text } = mfaResetEmail({
        appUrl: betterAuthUrl(),
        orgName: org.name ?? "your organization",
      });
      await sendEmail({ to: target.email, subject, html, text });
    } catch {
      // Notification is best-effort; sendEmail itself never throws on SMTP errors.
    }
  }

  revalidatePath("/members");
  revalidatePath("/audit");
  return { ok: true };
}

/**
 * Internal: revoke a just-removed user's live access. NOT a server action (not
 * exported) — it must only ever run with a userId that removeMemberAction has
 * already authorized, never a raw request input.
 */
async function finalizeMemberRevocation(
  userId: string,
): Promise<RevokeActionResult> {
  // 1. Kill the removed user's sessions so a still-valid session can't re-mint a JWT.
  try {
    const ctx = await auth.$context;
    // Better Auth 1.6 renamed the "delete all of a user's sessions" call to
    // deleteUserSessions (deleteSessions now takes specific session tokens).
    await ctx.internalAdapter.deleteUserSessions(userId);
  } catch {
    // Best-effort: a failed session delete still leaves the edge denylist + the live
    // membership re-check in force (gated access is revoked); it only leaves a ≤10m
    // dashboard-JWT window. Fall through to the authoritative denylist write.
  }
  // 2. Edge denylist write, the authoritative, edge-enforced revocation.
  return revokeAccessAction({ kind: "user", id: userId });
}
