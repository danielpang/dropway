"use client";

import { createAuthClient } from "better-auth/react";
import {
  magicLinkClient,
  organizationClient,
} from "better-auth/client/plugins";

/**
 * Better Auth React client. The client plugins must mirror the server plugins
 * (lib/auth.ts) that expose client-callable actions: organization + magic link.
 * The jwt plugin has no client surface (tokens are minted server-side / fetched
 * from the session), so it is intentionally omitted here.
 */
export const authClient = createAuthClient({
  baseURL:
    process.env.NEXT_PUBLIC_BETTER_AUTH_URL ??
    process.env.BETTER_AUTH_URL ??
    undefined,
  plugins: [organizationClient(), magicLinkClient()],
});

export const {
  signIn,
  signUp,
  signOut,
  useSession,
  getSession,
} = authClient;
