import "server-only";

import { auth } from "@/lib/auth";

/**
 * Whether the user behind these request headers has a PASSWORD credential.
 * Google-only / magic-link users have none, and every two-factor endpoint
 * skips the password check for them (allowPasswordless) — so the enroll UIs
 * must skip the password prompt too. One helper so the rule can't drift
 * between the security page and the mandatory setup page. Fails soft to
 * false (no accounts listed → no password prompt; the server still enforces).
 */
export async function userHasPasswordCredential(
  requestHeaders: Headers,
): Promise<boolean> {
  const accounts = await auth.api
    .listUserAccounts({ headers: requestHeaders })
    .catch(() => []);
  return accounts.some(
    (a: { providerId: string }) => a.providerId === "credential",
  );
}

/**
 * Server-side reads/writes of two-factor enrollment state, through Better
 * Auth's own adapter (the identity schema's owner) rather than raw SQL — the
 * same data layer the twoFactor plugin uses, so model/table name mapping and
 * pooling are inherited.
 *
 * The FRESH reads exist because the session cookie cache (5 min) can hold a
 * stale `twoFactorEnabled`: right after enrolling, the cached session still
 * says false, and the enforcement gate would bounce an enrolled user back to
 * setup. Enforcement paths must confirm against the live user row before
 * redirecting.
 */

/** Live per-user enrollment map for a set of user ids (the members page). */
export async function mfaEnabledByUser(
  userIds: string[],
): Promise<Record<string, boolean>> {
  if (userIds.length === 0) return {};
  const ctx = await auth.$context;
  const users = await ctx.adapter.findMany<{
    id: string;
    twoFactorEnabled: boolean | null;
  }>({
    model: "user",
    where: [{ field: "id", operator: "in", value: userIds }],
    limit: userIds.length,
  });
  return Object.fromEntries(users.map((u) => [u.id, u.twoFactorEnabled === true]));
}

/** Live enrollment check for one user (the enforcement gate's fresh read). */
export async function userTwoFactorEnabled(userId: string): Promise<boolean> {
  const ctx = await auth.$context;
  const user = await ctx.adapter.findOne<{ twoFactorEnabled: boolean | null }>({
    model: "user",
    where: [{ field: "id", value: userId }],
  });
  return user?.twoFactorEnabled === true;
}

/**
 * Clear a user's second factor entirely — TOTP secret, backup codes, the
 * enabled flag — and kill their sessions so re-enrollment happens on a fresh
 * sign-in. The LOCKOUT RECOVERY path: an owner/admin resets a member who lost
 * their authenticator; under org enforcement they're steered straight into
 * setup at their next sign-in.
 *
 * AUTHORIZATION IS THE CALLER'S JOB: this must only run with a userId the
 * calling server action has already verified to be a non-owner member of the
 * caller's own org, with the caller an owner/admin of it (see
 * resetMemberMfaAction). Not exported to the client.
 */
export async function resetUserTwoFactor(userId: string): Promise<void> {
  const ctx = await auth.$context;
  await ctx.adapter.deleteMany({
    model: "twoFactor",
    where: [{ field: "userId", value: userId }],
  });
  await ctx.internalAdapter.updateUser(userId, { twoFactorEnabled: false });
  // Sessions die so a live session can't linger half-authenticated; the member
  // signs in again (and, under enforcement, lands in mandatory setup).
  await ctx.internalAdapter.deleteUserSessions(userId);
}
