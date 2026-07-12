import { createHmac } from "node:crypto";

import type { BetterAuthPlugin } from "better-auth";
import { createAuthMiddleware, isAPIError } from "better-auth/api";
import { deleteSessionCookie, expireCookie } from "better-auth/cookies";
import { generateRandomString } from "better-auth/crypto";

/**
 * Closes the 2FA bypass on REDIRECT sign-ins. Better Auth's twoFactor plugin only
 * challenges the JSON credential endpoints (/sign-in/email|username|phone-number):
 * a Google OAuth callback or a magic-link click lands a full session with NO
 * second-factor prompt, which would make enforced MFA decorative — an attacker
 * with mailbox access just uses the magic link. This plugin applies the exact
 * same challenge to those redirect flows.
 *
 * Mechanism (mirrors the twoFactor plugin's credential hook, byte for byte where
 * it matters — the /two-factor/verify-* endpoints must accept our challenge
 * cookie as if the plugin itself had set it):
 *   1. after a /callback/:provider or /magic-link/verify request that created a
 *      session for a user with twoFactorEnabled, honor a valid trust-device
 *      cookie (rotating it, like the plugin does) and let the session stand;
 *   2. otherwise delete the just-created session + cookie, store a `2fa-…`
 *      verification value, set the signed `two_factor` challenge cookie, and
 *      302 the browser to /two-factor instead of the original destination,
 *      carrying that destination as ?next= (path validated by the page).
 *
 * The cookie names ("two_factor", "trust_device"), identifier formats and
 * verification-value shape are the twoFactor plugin's own (dist/plugins/
 * two-factor/constant + index) — pinned at better-auth 1.6.19; re-verify on any
 * version bump.
 *
 * Register AFTER twoFactor() in the plugins array. Safe under the jiti migrate
 * loader: only better-auth subpath imports, no server-only modules.
 */

const TWO_FACTOR_COOKIE_NAME = "two_factor";
const TRUST_DEVICE_COOKIE_NAME = "trust_device";
/** Mirrors the twoFactor plugin default (30 days). */
const TRUST_DEVICE_MAX_AGE = 30 * 24 * 60 * 60;
/** Mirrors the twoFactor plugin's twoFactorCookieMaxAge default (10 minutes). */
const CHALLENGE_MAX_AGE = 600;

/**
 * HMAC-SHA256 → unpadded base64url, byte-identical to the twoFactor plugin's
 * createHMAC("SHA-256", "base64urlnopad").sign(secret, data) — Node's
 * "base64url" digest is unpadded — so trust-device tokens minted by either
 * side verify on the other.
 */
function signTrustToken(secret: string, data: string): string {
  return createHmac("sha256", secret).update(data).digest("base64url");
}

/**
 * The original post-sign-in destination, recovered from the redirect the handler
 * already produced (both endpoints finish with a thrown 302 whose Location is the
 * validated callbackURL). Reduced to a same-app path so the /two-factor page can
 * use it as ?next= without opening a redirect. Falls back to the app's default
 * landing page.
 */
function nextPathFrom(returned: unknown): string {
  let location: string | null = null;
  if (isAPIError(returned)) {
    const headers = (returned as { headers?: Headers }).headers;
    location = headers?.get?.("location") ?? null;
  }
  if (!location) return "/dashboard";
  if (location.startsWith("/") && !location.startsWith("//")) return location;
  try {
    const url = new URL(location);
    return url.pathname + url.search + url.hash;
  } catch {
    return "/dashboard";
  }
}

export function twoFactorRedirectGate(): BetterAuthPlugin {
  return {
    id: "two-factor-redirect-gate",
    hooks: {
      after: [
        {
          matcher(context) {
            const path = context.path;
            if (!path) return false;
            return (
              path.startsWith("/callback/") ||
              path.startsWith("/oauth2/callback/") ||
              path === "/magic-link/verify"
            );
          },
          handler: createAuthMiddleware(async (ctx) => {
            const data = ctx.context.newSession;
            if (!data) return;
            const user = data.user as {
              id: string;
              twoFactorEnabled?: boolean | null;
            };
            if (!user.twoFactorEnabled) return;

            // Trusted device: same check-and-rotate as the plugin's credential
            // hook, so a device trusted via either flow is honored by both.
            const trustCookie = ctx.context.createAuthCookie(
              TRUST_DEVICE_COOKIE_NAME,
              { maxAge: TRUST_DEVICE_MAX_AGE },
            );
            const trustValue = await ctx.getSignedCookie(
              trustCookie.name,
              ctx.context.secret,
            );
            if (trustValue) {
              const [token, trustId] = trustValue.split("!");
              if (token && trustId) {
                const expected = signTrustToken(
                  ctx.context.secret,
                  `${user.id}!${trustId}`,
                );
                if (token === expected) {
                  const record =
                    await ctx.context.internalAdapter.findVerificationValue(
                      trustId,
                    );
                  if (
                    record &&
                    record.value === user.id &&
                    record.expiresAt > new Date()
                  ) {
                    await ctx.context.internalAdapter.deleteVerificationByIdentifier(
                      trustId,
                    );
                    const newTrustId = `trust-device-${generateRandomString(32)}`;
                    const newToken = signTrustToken(
                      ctx.context.secret,
                      `${user.id}!${newTrustId}`,
                    );
                    await ctx.context.internalAdapter.createVerificationValue({
                      value: user.id,
                      identifier: newTrustId,
                      expiresAt: new Date(
                        Date.now() + TRUST_DEVICE_MAX_AGE * 1000,
                      ),
                    });
                    await ctx.setSignedCookie(
                      trustCookie.name,
                      `${newToken}!${newTrustId}`,
                      ctx.context.secret,
                      trustCookie.attributes,
                    );
                    return; // trusted → the session stands
                  }
                }
              }
              expireCookie(ctx, trustCookie);
            }

            // Challenge: revoke the session the handler just created and hand
            // the browser to the /two-factor page with the plugin's own
            // challenge cookie, so verify-totp / verify-backup-code complete it.
            deleteSessionCookie(ctx, true);
            await ctx.context.internalAdapter.deleteSession(data.session.token);
            ctx.context.setNewSession(null);

            const challengeCookie = ctx.context.createAuthCookie(
              TWO_FACTOR_COOKIE_NAME,
              { maxAge: CHALLENGE_MAX_AGE },
            );
            const identifier = `2fa-${generateRandomString(20)}`;
            await ctx.context.internalAdapter.createVerificationValue({
              value: user.id,
              identifier,
              expiresAt: new Date(Date.now() + CHALLENGE_MAX_AGE * 1000),
            });
            await ctx.setSignedCookie(
              challengeCookie.name,
              identifier,
              ctx.context.secret,
              challengeCookie.attributes,
            );

            const next = nextPathFrom(ctx.context.returned);
            throw ctx.redirect(`/two-factor?next=${encodeURIComponent(next)}`);
          }),
        },
      ],
    },
  };
}
