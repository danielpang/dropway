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
export const authClient = createAuthClient({
  baseURL:
    process.env.NEXT_PUBLIC_BETTER_AUTH_URL ??
    process.env.BETTER_AUTH_URL ??
    undefined,
  plugins: [organizationClient(), magicLinkClient(), oauthProviderClient()],
});

export const {
  signIn,
  signUp,
  signOut,
  useSession,
  getSession,
} = authClient;
