// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// RFC 8414 OAuth 2.0 Authorization Server Metadata, mounted at the root
// /.well-known/oauth-authorization-server so MCP clients can discover Dropway's
// authorization endpoints. An MCP client hits the MCP server, gets a 401 pointing
// at the MCP resource metadata, which lists this dashboard as the authorization
// server — the client then fetches this document to find the authorize/token/
// registration endpoints and runs the browser OAuth flow.

import { auth } from "@/lib/auth";
import { oauthProviderAuthServerMetadata } from "@better-auth/oauth-provider";

export const GET = oauthProviderAuthServerMetadata(auth);
