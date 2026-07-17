<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Auth audit: dashboard ⇄ API (July 2026)

An end-to-end audit of the user-auth path between the Next.js dashboard and the
Go API, grounded in the production incidents from PostHog error tracking. It
answers two questions: **which processes are brittle**, and **whether the
dashboard→API path should move to OAuth user tokens** (it should not — see the
bottom).

## Architecture (unchanged by this audit)

Two separate JWT trust domains, each with its own keypair, JWKS, and verifier:

1. **User/session JWT** — Better Auth's `jwt()` plugin mints a 10-minute EdDSA
   JWT server-side per request (`apps/dashboard/lib/api.ts`); the Go API and the
   MCP server verify it via `internal/auth` (JWKS from the dashboard, alg pinned
   to EdDSA, iss/aud pinned from `JWT_ISSUER`/`JWT_AUDIENCE`).
2. **Edge token** — the Go API's separate edge signer mints a 15-minute
   host-scoped token for gated content; the Cloudflare Worker and the Go `serve`
   service verify it (`internal/edgetoken`, `edge/serving-worker`).

OAuth 2.1 (the `oauthProvider` plugin) is used ONLY for third-party clients
(MCP connectors, CLI), as an authorization server the MCP resource server
points at.

## Incidents that motivated the audit

| Incident (PostHog) | Root cause |
|---|---|
| `claims missing user_id/org_id for RLS context` 500s (Jun 25 – Jul 6) | JWTs minted with an empty `org_id` (org backfill race / org-less session); enforcement happened deep in the store, not at the auth boundary. Patched three times in three weeks (6/20, 7/11 ×2) — symptom fixes on one root design issue. |
| `APIError: Unauthorized` noise (Jul 8–10) | A failed token mint was swallowed and the request sent **unauthenticated**; a 401 never invalidated the token cache or re-minted. |
| `EMAXCONNSESSION` on the identity DB | Session verification falling back to per-call Postgres reads (cookie-cache expiry), small pool vs pooler cap. |
| Contact form "From: unknown" (Jul 11) | Better Auth `get-session` throws on POST with the cookie cache enabled; server actions are POSTs. Fixed in #80 (`method: "GET"`). |

## Findings (ranked) and dispositions

**B1 — org identity is a mutable session field snapshotted into a bearer token,
enforced in five scattered places.** The session-create hook, `definePayload`'s
`firstOrgId` fallback, handler `tenant()`, store `SetTenantContext`, and the
`writeStoreError` mapping each re-check it, with inconsistent outcomes (401 vs
opaque 500).
→ **Fixed**: single authoritative check in the API's Auth middleware
(`internal/middleware/auth.go`) — a verified token with empty `sub`/`org_id` is
rejected at the boundary with a machine-readable
`401 {error:"reauth_required"}`. Later checks remain as defense-in-depth only.

**B2 — silent unauthenticated fallback + no 401 recovery.** A mint failure sent
the request with no Authorization header (guaranteed 401, error-tracking noise);
a 401 response never invalidated the cross-request token cache.
→ **Fixed**: `apiFetch` fails locally with the same `ApiError(401,
{error:"reauth_required"})` shape callers already handle, and on a real API 401
it drops the cache entry, re-mints once, and retries once (no retry when the
fresh token is identical or the session is dead).

**B3 — byte-identical env-string agreement across services**
(`JWT_ISSUER`/`JWT_AUDIENCE`/`MCP_PUBLIC_URL`), with `deploy/.env.example` as
the only contract. The dashboard registers six audience string forms, Go
centralizes four (`MCPResourceAudiences`) — scar tissue from commit `0d4621e`.
The API's boot guard catches *empty* pins (`config.go`), nothing catches
*mismatched* pins.
→ **Partially addressed**: cross-language parity tests
(`internal/edgetoken/parity_test.go`) pin the values that can drift silently
(see B6); iss/aud mismatch across deploy environments remains an operational
risk — mitigate with deploy-time smoke checks (`GET /v1/me` after deploy).

**B4 — zero clock-skew tolerance** on 10–15-minute tokens across three
verifiers on different clocks (Vercel, Fly, Cloudflare).
→ **Fixed**: shared 60s leeway — `auth.ClockSkewLeeway` in the user-JWT
verifier and the Go edge verifier, `clockTolerance: 60` in the Worker, with a
parity test asserting Go and Worker agree.

**B5 — four caches with divergent staleness windows** (session cookie cache
5m, token cache 4m, JWKS 15s-gate/5m+10m-grace, org cache 60s). Individually
justified; the composition is where the POST/GET session bug lived.
→ **Documented here; no change.** The #80 regression test pins the worst
interaction. Revocation exposure remains ≤10m by design (unrevocable JWT
lifetime), unchanged.

**B6 — duplicated constants with no compile-time link.** The edge issuer is
hard-coded in Go (`internal/edgetoken/edgetoken.go`) and TS
(`edge/serving-worker/src/config.ts`); an unset `EDGE_SIGNING_KEY` silently
minted an ephemeral key per restart, invalidating every gated-content session
on each deploy.
→ **Fixed**: parity tests fail the build on issuer/tolerance drift; with
`ENVIRONMENT=production` the API now refuses to boot without
`EDGE_SIGNING_KEY`.

**Sound and kept as-is**: EdDSA alg pinning; JWKS empty-keyset + refresh
rate-limit guards; live-membership re-checks for admin/mint (the JWT `role`
claim is never trusted); fail-closed `min_iat` revocation denylist; boot guard
against empty iss/aud pins.

## Should dashboard→API auth move to OAuth user tokens? No.

Evaluated: the dashboard becoming an OAuth client of its own authorization
server, attaching OAuth access tokens (+ refresh tokens) to API calls instead
of the `jwt()` plugin's session JWTs.

1. **It fixes none of the observed failures.** OAuth access tokens carry the
   same `org_id` claim (via the same `firstOrgId`, with *less* session
   context), the same iss/aud pinning against the same Go verifier, the same
   clock-skew exposure. B1–B4 all survive the migration.
2. **It moves the critical path onto the most-patched surface.** The audience
   string-form hacks, the consent redirect bug, and the DCR rate-limit
   hardening all live in the OAuth stack — it is the newer, less battle-tested
   plugin.
3. **It adds real machinery with new failure modes**: refresh-token storage and
   rotation (more identity-DB load — the `EMAXCONNSESSION` axis), auth-code
   round trips, consent suppression for a first-party client.
4. **The current shape is the standard BFF pattern** — cookie session at the
   first-party server, short-lived signed JWT service-to-service. The
   architecture is right; the brittleness was in claim lifecycle, config drift,
   and error recovery, which are addressed above.

OAuth stays exactly where it belongs today: third-party MCP/CLI clients.

## Operational follow-ups (not code)

- Add a post-deploy smoke check that exercises `GET /v1/me` with a freshly
  minted token (catches iss/aud/JWKS drift the moment it ships).
- Consider durable (database/WAF) rate limiting for the unauthenticated OAuth
  endpoints; the current in-memory limiter is per-instance.
- Watch `EMAXCONNSESSION`: if it recurs, the next lever is Better Auth's
  cookie-cache maxAge vs the `SessionCacheRefresh` ping interval, not pool size.
