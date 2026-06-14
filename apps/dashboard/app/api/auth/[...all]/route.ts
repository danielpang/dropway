import { toNextJsHandler } from "better-auth/next-js";

import { auth } from "@/lib/auth";

/**
 * Mounts Better Auth's handler at /api/auth/* — sign-in/up, OAuth callbacks,
 * magic-link, the organization endpoints, AND the JWKS endpoint the Go API uses
 * to verify EdDSA JWTs (architecture §3).
 */
export const { GET, POST } = toNextJsHandler(auth);
