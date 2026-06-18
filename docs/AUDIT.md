<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Code audit: findings and disposition

A full audit of the Phases 0 through 4 codebase (Go + TypeScript) for bugs, dead code,
silent failures, and security/tenant-isolation regressions, run alongside the
coverage push (`docs/COVERAGE.md`). Each finding's status is one of: **FIXED**,
**ACCEPTED** (intentional or low risk, kept as-is), or **DEFERRED** (tracked
follow-up).

## Fixed

| Sev | Area | Where | Finding → fix |
|---|---|---|---|
| **HIGH** | auth | `services/api/internal/config/config.go` | golang-jwt **skips iss/aud validation when the expected value is empty**, so an empty `JWT_ISSUER`/`JWT_AUDIENCE` would accept *any* EdDSA-signed unexpired token. Fix: `Load()` now **fails fast** when `JWKS_URL` is set but either is empty (`config_validate_test.go`). |
| **MEDIUM** | dead code | `services/api/internal/handlers/sites.go` | Duplicate `case errors.Is(err, store.ErrHostTaken)` in `writeStoreError`; the second was unreachable. Fix: removed. |
| **MEDIUM** | test quality | `apps/dashboard/lib/audit.ts` | `isSecurityAction` silently **failed to highlight the real `site.access_change`** action (the `\b` after "access" doesn't match `_change`); the test only asserted fabricated strings. Fix: widened the pattern and added `test/audit-canonical.test.ts` asserting the **canonical Go action vocabulary** (mirrors `internal/audit`). |
| **LOW** | store RLS | `services/api/internal/store/authz.go` | `ResolveForPassword` set `app.current_user_id` to the **org id** (password mode is anonymous). Benign now, latent mis-scoping trap. Fix: set the user GUC empty. |

## Accepted (intentional / low risk)

| Sev | Area | Where | Note |
|---|---|---|---|
| LOW | unused exports | `store.PreflightMembers`, `store.GetOrgPolicy` | Kept for the planned members-cap preflight endpoint; no behavior risk. Remove if the endpoint is dropped. |
| MEDIUM | edge suspension binding | `edge/serving-worker/wrangler.toml` | The `LIMITS` KV binding is commented (it needs a per-deploy namespace id). Without it, edge **rate-limiting fails open** and `org_status` suspension is inert. By design, the binding is a deploy requirement. **Operators must bind `LIMITS` in production.** (Hard revocation is separate and fails *closed*.) |

## Deferred (tracked follow-ups)

| Sev | Area | Where | Finding |
|---|---|---|---|
| LOW | gc retention | `services/api/internal/store/gc.go` | `selectRetained` can keep N-1 (not N) non-current versions when the live version is in the newest N. This is a retention-window off-by-one, **not** data corruption. |
| LOW | edge cache key | `edge/serving-worker/src/index.ts` | A `public` to `gated` access flip on the **same `version_id`** can leave already-cached public bytes in the shared Cache API. Mitigation: key the cache by `access_mode` (or rotate the version id on a tighten). Note: access-tighten already writes a `revoked:site` denylist entry. |
| LOW | edge headers | `edge/serving-worker/src/index.ts` | The `405` branch uses the permissive tenant CSP rather than `platformSecurityHeaders`; plus a dead `applyHeaders` import / empty `serviceWorkerBlockHeaders`. |
| LOW | dashboard billing | `apps/dashboard/.../finalizing-state.tsx` | The post-checkout poller can time out on a **same-tier** webhook (key it off subscription status, not tier change). |
| LOW | dashboard client | `apps/dashboard/lib/auth-client.ts` | `baseURL` falls back to the non-public `BETTER_AUTH_URL` (undefined in the browser); prefer only `NEXT_PUBLIC_BETTER_AUTH_URL`. |
| LOW | contracts | `contracts/src/index.ts` | TS `expires_at` uses lenient `Date.parse` vs Go's strict RFC3339; tighten the TS validator. |

## Invariants re-verified (held)

The audit confirmed the load-bearing invariants are intact: every request DB path
runs as the non-BYPASSRLS `dropway_app` role under a `SET LOCAL` tenant context
(no superuser/bypass on request paths); the public Worker path is JWT-free; the
paid `plan_tier` is written **only** by the signature-verified Stripe webhook; the
hard-revocation denylist **fails closed**; the OSS build links **zero**
`cloud/`/`ee/`/`stripe` symbols. See `docs/ARCHITECTURE.md` and the integration +
RLS-policy test suites.
