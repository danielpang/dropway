// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// E2E coverage for the OAuth 2.1 provider endpoints that back MCP connect and CLI
// login — the layer the Go MCP integration test can't reach (it mints JWTs
// directly and never runs the authorization-server handshake). These drive the
// real endpoints over HTTP against the running dashboard:
//
//   - GET  /.well-known/oauth-authorization-server  → RFC 8414 metadata
//   - POST /api/auth/oauth2/register                 → RFC 7591 Dynamic Client Reg
//   - GET  /api/auth/oauth2/authorize                → the authorize + scope check
//
// They exist because a scope mismatch here (the `invalid_scope: offline_access`
// connect regression) had ZERO test coverage: nothing exercised DCR → authorize.
// The scope cases below lock in both the fix (offline_access is a valid MCP scope)
// and the graceful-narrowing behavior (an unsupported scope is dropped, not fatal).
//
// Rate limits shape the structure here: unauthenticated DCR is capped at 5/hour/IP
// (lib/oauth-ratelimit.ts), and the e2e dashboard runs in production mode where the
// limit is live. So the two clients these tests need are registered ONCE in
// beforeAll and shared — re-registering per test would exhaust the budget (429).

import { createHash, randomBytes } from "node:crypto";

import {
  expect,
  request as playwrightRequest,
  test,
  type APIRequestContext,
} from "@playwright/test";

const REDIRECT_URI = "http://localhost:9999/callback";
const BASE_URL = process.env.E2E_BASE_URL ?? "http://localhost:3000";

interface RegisteredClient {
  client_id: string;
  scope?: string;
}

/** A fresh PKCE challenge (public clients require S256). */
function pkceChallenge(): string {
  const verifier = randomBytes(32).toString("base64url");
  return createHash("sha256").update(verifier).digest("base64url");
}

/** Dynamically register a public MCP-style client, optionally with a scope string. */
async function registerClient(
  request: APIRequestContext,
  scope?: string,
): Promise<RegisteredClient> {
  const res = await request.post("/api/auth/oauth2/register", {
    data: {
      client_name: "E2E OAuth Test Client",
      redirect_uris: [REDIRECT_URI],
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      token_endpoint_auth_method: "none",
      ...(scope ? { scope } : {}),
    },
  });
  // RFC 7591 says 201, but Better Auth returns 200 — assert success, not a code.
  expect(res.ok(), await res.text()).toBeTruthy();
  return res.json();
}

/** Build an /authorize URL for a client + requested scope, with PKCE + state. */
function authorizeUrl(clientId: string, scope: string): string {
  const params = new URLSearchParams({
    response_type: "code",
    client_id: clientId,
    redirect_uri: REDIRECT_URI,
    scope,
    code_challenge: pkceChallenge(),
    code_challenge_method: "S256",
    state: "e2e-state",
  });
  return `/api/auth/oauth2/authorize?${params.toString()}`;
}

/** Hit /authorize WITHOUT following the redirect, returning its Location. */
async function authorizeLocation(
  request: APIRequestContext,
  clientId: string,
  scope: string,
): Promise<string> {
  const res = await request.get(authorizeUrl(clientId, scope), {
    maxRedirects: 0,
  });
  // No session in this context, so a VALID authorize redirects to the login page
  // and a REJECTED one redirects back to the client with ?error=... — either way
  // it's a 3xx we inspect rather than follow.
  expect(
    res.status(),
    `expected a redirect, got ${res.status()}`,
  ).toBeGreaterThanOrEqual(300);
  expect(res.status()).toBeLessThan(400);
  return res.headers()["location"] ?? "";
}

test.describe("OAuth 2.1 provider endpoints", () => {
  // The two clients the scope tests need, registered ONCE (DCR is 5/hour/IP):
  //  - `bothClient` asked for both supported scopes AND an unsupported one; its
  //    stored scope proves narrowing, and it's reused for the authorize tests.
  //  - `mcpOnlyClient` registered only "mcp", for the "can't exceed registration".
  let apiContext: APIRequestContext;
  let bothClient: RegisteredClient;
  let mcpOnlyClient: RegisteredClient;

  test.beforeAll(async () => {
    apiContext = await playwrightRequest.newContext({ baseURL: BASE_URL });
    bothClient = await registerClient(
      apiContext,
      "mcp offline_access urn:example:unsupported",
    );
    mcpOnlyClient = await registerClient(apiContext, "mcp");
  });

  test.afterAll(async () => {
    await apiContext.dispose();
  });

  test("authorization-server metadata advertises the endpoints and MCP scopes", async ({
    request,
  }) => {
    const res = await request.get("/.well-known/oauth-authorization-server");
    expect(res.ok()).toBeTruthy();
    const meta = await res.json();
    expect(meta.authorization_endpoint).toContain("/oauth2/authorize");
    expect(meta.token_endpoint).toContain("/oauth2/token");
    // DCR must be advertised — the MCP "paste a URL" UX depends on it.
    expect(meta.registration_endpoint).toContain("/oauth2/register");
    expect(meta.code_challenge_methods_supported).toContain("S256");
    // Both the custom MCP scope and offline_access (refresh tokens) must be offered.
    expect(meta.scopes_supported).toEqual(
      expect.arrayContaining(["mcp", "offline_access"]),
    );
  });

  test("DCR narrows an unsupported scope instead of failing registration", async () => {
    // bothClient asked for "mcp offline_access urn:example:unsupported"; the
    // unsupported scope must be dropped (graceful narrowing) rather than 400-ing
    // the whole registration, leaving only the two supported scopes.
    const scope = bothClient.scope ?? "";
    expect(bothClient.client_id).toBeTruthy();
    expect(scope.split(" ")).toEqual(
      expect.arrayContaining(["mcp", "offline_access"]),
    );
    expect(scope).not.toContain("urn:example:unsupported");
  });

  test("authorize accepts mcp + offline_access (offline_access connect regression)", async ({
    request,
  }) => {
    // The exact scope combo Claude's connector requests. Registered for both, the
    // authorize step must NOT reject it — it should fall through to the login page.
    const location = await authorizeLocation(
      request,
      bothClient.client_id,
      "mcp offline_access",
    );
    expect(location).not.toContain("error=invalid_scope");
    expect(location).not.toContain(`${REDIRECT_URI}?error`);
  });

  test("authorize narrows an unsupported scope instead of invalid_scope", async ({
    request,
  }) => {
    // Requesting a bogus scope on top of registered ones is narrowed away, so the
    // handshake proceeds (to login) rather than dead-ending with invalid_scope.
    const location = await authorizeLocation(
      request,
      bothClient.client_id,
      "mcp offline_access urn:example:unsupported",
    );
    expect(location).not.toContain("error=invalid_scope");
  });

  test("authorize still rejects a supported scope the client never registered", async ({
    request,
  }) => {
    // Narrowing drops UNSUPPORTED scopes; it must NOT let a client exceed what it
    // registered. mcpOnlyClient registered only "mcp", so requesting offline_access
    // (which IS a supported scope, hence not narrowed away) is correctly rejected —
    // which is precisely why the MCP resource metadata must advertise offline_access
    // up front, so real clients register for it.
    const location = await authorizeLocation(
      request,
      mcpOnlyClient.client_id,
      "mcp offline_access",
    );
    expect(location).toContain("error=invalid_scope");
  });
});
