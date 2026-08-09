// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Pins the MCP OAuth "connect once" TTL policy. Short access-token lifetimes
// force frequent refresh; Better Auth's refresh-token family revocation then
// kills the grant and users have to re-authorize to create/deploy sites.

import { describe, expect, it } from "vitest";

import {
  MCP_OAUTH_ACCESS_TOKEN_EXPIRES_IN,
  MCP_OAUTH_EXPIRES_IN_MAX_SAFE,
  MCP_OAUTH_REFRESH_TOKEN_EXPIRES_IN,
} from "@/lib/mcp-oauth-ttl";

describe("MCP OAuth TTL policy", () => {
  it("mints effectively non-expiring access and refresh tokens", () => {
    const tenYears = 60 * 60 * 24 * 365 * 10;
    expect(MCP_OAUTH_ACCESS_TOKEN_EXPIRES_IN).toBeGreaterThanOrEqual(tenYears);
    expect(MCP_OAUTH_REFRESH_TOKEN_EXPIRES_IN).toBeGreaterThanOrEqual(tenYears);
    expect(MCP_OAUTH_ACCESS_TOKEN_EXPIRES_IN).toBe(
      MCP_OAUTH_REFRESH_TOKEN_EXPIRES_IN,
    );
  });

  it("stays under signed 32-bit expires_in so MCP clients don't overflow", () => {
    expect(MCP_OAUTH_ACCESS_TOKEN_EXPIRES_IN).toBeLessThanOrEqual(
      MCP_OAUTH_EXPIRES_IN_MAX_SAFE,
    );
    expect(MCP_OAUTH_REFRESH_TOKEN_EXPIRES_IN).toBeLessThanOrEqual(
      MCP_OAUTH_EXPIRES_IN_MAX_SAFE,
    );
  });
});
