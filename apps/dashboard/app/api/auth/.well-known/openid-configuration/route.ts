// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// OpenID Connect Discovery metadata, ALSO served under the Better Auth base path
// (/api/auth/.well-known/openid-configuration) — the OIDC sibling of the
// oauth-authorization-server doc in this same directory. Clients that probe the
// /api/auth base for discovery often try BOTH the RFC 8414 and OIDC documents;
// serving both here avoids a 404 fallback. Literal route, so it wins over the
// [...all] catch-all for this exact path. Identical content to the root copy.

import { auth } from "@/lib/auth";
import { oauthProviderOpenIdConfigMetadata } from "@better-auth/oauth-provider";

export const GET = oauthProviderOpenIdConfigMetadata(auth);
