// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// RFC 8414 Authorization Server Metadata, ALSO served under the Better Auth base
// path (/api/auth/.well-known/oauth-authorization-server), not just the root
// /.well-known/... copy. Better Auth's OAuth endpoints live under /api/auth, and
// some MCP/OAuth clients derive the metadata location from that base rather than
// the issuer root, then 404 here. This literal route takes precedence over the
// catch-all [...all] handler for this exact path; everything else under /api/auth
// still flows through Better Auth. The document is identical to the root copy
// (the endpoint URLs inside are absolute), generated from the same provider config.

import { auth } from "@/lib/auth";
import { oauthProviderAuthServerMetadata } from "@better-auth/oauth-provider";

export const GET = oauthProviderAuthServerMetadata(auth);
