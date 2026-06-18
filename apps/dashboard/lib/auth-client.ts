"use client";

import { createAuthClient } from "better-auth/react";
import {
  magicLinkClient,
  organizationClient,
} from "better-auth/client/plugins";
import { oauthProviderClient } from "@better-auth/oauth-provider/client";

/**
 * Better Auth React client. The client plugins must mirror the server plugins
 * (lib/auth.ts) that expose client-callable actions: organization, magic link, and
 * the OAuth provider (its `oauth2.consent` action backs the MCP "Authorize" page).
 * The jwt plugin has no client surface (tokens are minted server-side / fetched
 * from the session), so it is intentionally omitted here.
 */
const baseURL =
  process.env.NEXT_PUBLIC_BETTER_AUTH_URL ??
  process.env.BETTER_AUTH_URL ??
  undefined;

export const authClient = createAuthClient({
  baseURL,
  plugins: [organizationClient(), magicLinkClient(), oauthProviderClient()],
});

/**
 * A dedicated client for the OAuth consent screen. `disableDefaultFetchPlugins`
 * drops Better Auth's `redirectPlugin` — the ONLY default fetch plugin — which would
 * otherwise auto-navigate the browser to the client's redirect_uri the instant
 * `oauth2.consent` resolves (its response carries `{ url, redirect: true }`).
 * Suppressing it lets /oauth/consent show a branded "Authorization successful"
 * screen and then perform the redirect itself. Scoped to the consent page so the
 * global authClient keeps the default auto-redirect every other flow relies on.
 */
export const oauthConsentClient = createAuthClient({
  baseURL,
  disableDefaultFetchPlugins: true,
  plugins: [oauthProviderClient()],
});

export const {
  signIn,
  signUp,
  signOut,
  useSession,
  getSession,
} = authClient;
