// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Pins the dashboard-side MCP audience list to the same four canonical forms
// Go's `internal/auth.MCPResourceAudiences` returns. Omitting `.../mcp` here is
// what made Claude/Cursor token exchanges fail with `invalid_request`
// ("requested resource invalid") in production.

import { describe, expect, it } from "vitest";

import {
  mcpResourceAudiences,
  oauthProviderValidAudiences,
} from "@/lib/mcp-oauth-audiences";

const MCP_BASE = "https://mcp.dropway.dev";
const MCP_FORMS = [
  "https://mcp.dropway.dev",
  "https://mcp.dropway.dev/",
  "https://mcp.dropway.dev/mcp",
  "https://mcp.dropway.dev/mcp/",
] as const;

describe("mcpResourceAudiences", () => {
  it("lists the four canonical resource forms and ignores a trailing slash on input", () => {
    for (const input of [MCP_BASE, `${MCP_BASE}/`]) {
      expect(mcpResourceAudiences(input)).toEqual([...MCP_FORMS]);
    }
  });

  it("includes the connector URL Claude sends as the RFC 8707 resource", () => {
    expect(mcpResourceAudiences(MCP_BASE)).toContain(`${MCP_BASE}/mcp`);
  });
});

describe("oauthProviderValidAudiences", () => {
  it("unions MCP forms from both URL sources with the API jwt audience", () => {
    const audiences = oauthProviderValidAudiences({
      mcpResourceUrl: MCP_BASE,
      mcpUrl: MCP_BASE,
      jwtAudience: "https://api.dropway.dev",
    });

    for (const form of MCP_FORMS) {
      expect(audiences).toContain(form);
    }
    expect(audiences).toContain("https://api.dropway.dev");
    expect(audiences).toContain("https://api.dropway.dev/");
    // Identical MCP sources must not duplicate the four forms.
    expect(audiences.filter((a) => a.startsWith(MCP_BASE))).toHaveLength(4);
  });

  it("keeps both MCP URL sources when they differ (self-host / internal vs public)", () => {
    const audiences = oauthProviderValidAudiences({
      mcpResourceUrl: "https://mcp.internal",
      mcpUrl: "https://mcp.dropway.dev",
      jwtAudience: "https://api.dropway.dev",
    });

    expect(audiences).toContain("https://mcp.internal/mcp");
    expect(audiences).toContain("https://mcp.dropway.dev/mcp");
  });
});
