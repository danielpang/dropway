// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// OpenID Connect Discovery metadata, mounted at the root
// /.well-known/openid-configuration. This is the OIDC sibling of the RFC 8414
// /.well-known/oauth-authorization-server document: it advertises the same
// authorize/token/jwks/registration endpoints plus the OIDC-specific bits
// (userinfo_endpoint, id_token_signing_alg_values_supported, claims, …).
//
// Why both: MCP clients (Claude, mcp-remote, the MCP SDK) discover the auth
// server from the resource metadata, then probe for AUTHORIZATION-server metadata.
// Strict RFC 8414 clients use /.well-known/oauth-authorization-server, but many
// fall back to (or prefer) OIDC discovery at /.well-known/openid-configuration.
// Serving only the former left this 404, breaking those clients, so we expose
// both from the same Better Auth provider config.

import { auth } from "@/lib/auth";
import { oauthProviderOpenIdConfigMetadata } from "@better-auth/oauth-provider";

export const GET = oauthProviderOpenIdConfigMetadata(auth);
