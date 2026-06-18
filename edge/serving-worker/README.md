<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# @dropway/serving-worker

Cloudflare Worker that serves tenant static sites on `*.dropwaycontent.com`
(the PSL-listed content domain, never a subdomain of the app/auth domain). It
serves both the **PUBLIC** path (the 95% case: **JWT-free and cacheable**) and
the **Phase-2 GATED** path (`password` | `allowlist` | `org_only`) via the
host-scoped **edge token** plus `/authz` exchange.

The Worker is a **thin router and a read-only consumer** of its bindings. The
Go API (`api.dropway.dev`) is the real authz boundary and the **sole writer** of
the KV/R2 projections this Worker reads; everything here is fully rebuildable
from Postgres.

## What it does (Phase 1)

```
GET https://<host>/<path>
  → KV ROUTES.get("route:<host>") → { org_id, site_id, version_id,
                                      access_mode, schema_version }
  → access_mode === "public":
        fetch the deploy manifest from R2 at
          manifests/<org_id>/<site_id>/<version_id>.json   (path → {sha256,content_type})
        resolve <path> (index.html + directory + .html fallback) to an entry
        stream the content-addressed blob from R2 at
          blobs/<org_id>/<sha256>
        with:
          - Content-Type from the MANIFEST (authoritative; bytes are not re-sniffed)
          - Cache-Control: immutable for hashed assets, short TTL for HTML
          - custom 404.html (or a default) when nothing matches
          - successful responses written to the Cache API (per-version keyed)
  → access_mode === "password" | "allowlist" | "org_only":   [Phase 2]
        read the __Host-edge cookie → verify the edge token (jose) against the
          API's edge JWKS (EDGE_JWKS_URL; alg pinned EdDSA, iss + aud==host +
          exp + site_id==route.site_id)
        valid  → serve the SAME manifest→blob bytes, but PRIVATE
                   (Cache-Control: private, no-store; never the public Cache API)
        absent/invalid → 302 https://app.dropway.dev/authz?host=<host>&next=<path>
                            (APP_AUTHZ_URL; the dashboard runs the exchange)

  → expires_at (v2 RouteValue) in the past → 410 platform "link expired" page
```

The Worker **never reads the operator Better Auth JWT**, only the host-scoped
edge token (cookie). The public path is JWT-free; any `Authorization` header is
ignored.

### The `/authz` exchange (gated tiers, Phase 2)

```
Worker (no/invalid __Host-edge)  ──302──►  app.dropway.dev/authz?host=&next=
   dashboard: require Better Auth session, then
     org_only/allowlist → POST api /v1/authz/mint {host,next}      → {token} | 403
     password           → platform password form → POST /v1/authz/password → {token}
   dashboard ──302──►  https://<host>/__authz/callback?token=&next=
Worker GET /__authz/callback:
   verify token (aud==host, site_id==route) → Set-Cookie __Host-edge
     (host-only, Secure, HttpOnly, SameSite=Lax) → 302 to a SAFE same-host `next`
     (off-host / protocol-relative / backslash / CRLF `next` collapses to "/")
```

**Edge token** (mirrors the Go `internal/edgetoken` signer): a compact **EdDSA**
JWT, a **separate keypair** from Better Auth's user JWT.
`iss=https://api.dropway.dev/edge`, `aud=<content host>`,
`sub=<user_id>` (org_only/allowlist) or `anon:<random>` (password),
`exp=now+15m`, plus `{ site_id, mode }`. The Worker pins `alg=EdDSA` (rejects
`none`/HS\*), checks `iss`, `aud==request host`, `exp`, and that `site_id`
matches the route. The JWKS is fetched from `EDGE_JWKS_URL` and cached per
isolate (5-min TTL; a transient JWKS outage falls back to the last-good keys).

### Path resolution rules

- Root (`/`) and directory paths (`/blog/`) → `…/index.html`.
- Explicit asset paths (`/assets/app.css`) → served directly.
- Extension-less "pretty" paths (`/about`) → try, in order:
  `about`, `about/index.html`, `about.html`.
- Path traversal (`..`, encoded `..`, NUL, backslash, malformed `%`-encoding)
  is rejected and **fails closed to a 404**: a request can never escape its
  version prefix into another org/site/version.

### Cache policy (`src/http.ts`)

| Asset class                                  | `Cache-Control`                          |
| -------------------------------------------- | ---------------------------------------- |
| Content-hash fingerprinted (`app.4f3a9c2b.js`) | `public, max-age=31536000, immutable`    |
| HTML / non-hashed assets                     | `public, max-age=60, must-revalidate`    |
| Gated (password/allowlist/org_only) responses | `private, no-store, max-age=0, must-revalidate` + `Vary: Cookie` (never shared-cached) |
| Expired link (`410`)                          | `no-store`                               |
| Platform pages (`429` / `503`)                | `no-store` (+ `Retry-After`)             |

Every response (public, gated, **and** the platform 404/410/429/503 pages)
carries defense-in-depth content-security headers; see **Edge hardening
(Phase 4)** below. CSP is **not** the isolation control here. Domain/PSL
separation is.

## Edge hardening (Phase 4)

Three launch-blocking controls live entirely at the edge. All are driven by
injected KV/clock so they unit-test without a live
edge (`src/security.ts`, `src/ratelimit.ts`, `src/revoke.ts`).

### 1. Content-security headers (every response)

`src/security.ts` defines two header sets, both applied via `securityHeaders()`
(content) and `platformSecurityHeaders()` (our own pages):

| Header | Content (tenant bytes) | Platform pages (404/410/429/503) |
| --- | --- | --- |
| `Content-Security-Policy` | **permissive-but-safe** default (see below) | **strict** `default-src 'none'`, no scripts |
| `X-Content-Type-Options` | `nosniff` | `nosniff` |
| `Referrer-Policy` | `no-referrer` | `no-referrer` |
| `X-Frame-Options` | `DENY` | `DENY` |
| `Cross-Origin-Opener-Policy` | `same-origin` | `same-origin` |
| `Cross-Origin-Resource-Policy` | `same-site` | `same-site` |

The **default content CSP** is deliberately permissive enough that an ordinary
static site (its own HTML/CSS/JS, **inline** scripts/styles, `data:`/`blob:`
images & fonts, `eval`/`blob:` workers, XHR/fetch to self + any `https:`) keeps
working, while denying the dangerous primitives: `frame-ancestors 'none'`
(clickjacking), `base-uri 'self'` + `form-action 'self'`, `object-src 'none'`.
CSP is **defense in depth, not the isolation boundary** (the separate PSL
content domain is), so we optimize for "static sites just work" and leave
a stricter per-site CSP as a future opt-in. `CONTENT_CSP` / `PLATFORM_CSP` are
exported and the exact string is asserted in tests.

`Cross-Origin-Resource-Policy: same-site` (not `same-origin`) is deliberate: a
site legitimately loads its own subdomain assets, but a **different registrable
site** cannot embed a tenant resource as a subresource.

### 2. Service-worker registration blocked on content origins

`isServiceWorkerScript()` recognizes the conventional SW script names (`sw.js`,
`service-worker.js`, `ngsw-worker.js`, …) and the Worker **refuses to serve a
scriptable body** at those paths (returns the platform 404), so a tenant can
never register a service worker that would persist its JS, intercept fetches, or
survive a takedown. Enforced on **both** the public and gated paths.

### 3. Edge rate limiting + denial-of-wallet

- **Per-(IP|host) rate limit**: a fixed-window KV counter
  (`rateLimitDecision()`, pure over an injected counter store + clock) checked
  **before** the route lookup so a flood is rejected for one KV op. Over the
  limit → **`429` with `Retry-After`**. Identity is `CF-Connecting-IP` (else the
  host). Fails **open** (a missing `LIMITS` binding or KV error never blocks a
  real request); the authoritative spend caps live in the Go API. Tunable via
  `RATE_LIMIT_MAX` / `RATE_LIMIT_WINDOW_SECONDS` (default 600 req / 60 s).
- **Per-org suspension / over-limit**: after the route is resolved, the Worker
  reads `org_status:<org_id>` from KV; `suspended` (billing/abuse) or
  `over_limit` (quota/egress cap) → a platform **`503` "account unavailable"
  page** instead of **any** tenant content (public or gated), with
  `Retry-After: 300`. Skipped when no status KV is configured; fails **open** on
  a miss/error (the Go API + billing stay authoritative).

### 4. Hard revocation: KV denylist / `min_iat` (gated path)

After the edge token verifies, the gated path consults the **revocation
denylist** (`src/revoke.ts`) per the revocation contract:

```
revoked:user:<sub>   → { "min_iat": <unix seconds> }
revoked:site:<siteId>→ { "min_iat": <unix seconds> }
revoked:org:<orgId>  → { "min_iat": <unix seconds> }     # org from the ROUTE, not a token claim
```

If **any** dimension has `min_iat > token.iat`, the token is treated as invalid
→ **302 to `/authz`** (re-auth). This makes a ban / unshare / org-suspension
take effect **immediately** instead of waiting out the 15-minute token TTL (the
TTL is the backstop). The denylist is **rebuildable from Postgres** and only
ever fails **closed**: a stale or unavailable denylist causes at most an extra
re-auth, never opens access (a missing denylist binding or a KV read error →
treated as revoked). The Go API is the **sole writer** (Phase-4 backlog). The
**public path never consults the denylist** (it is identity-free).

The denylist KV defaults to the **ROUTES namespace with the `revoked:` prefix**;
supply a dedicated `REVOKED` binding to isolate it. The edge token now carries
`iat` (already minted by the Go signer) which `verifyEdgeToken()` surfaces for
this comparison.

## The cross-language contract

The KV value at `route:<host>` is the one genuine Go↔TS data contract:

```ts
interface RouteValue {
  org_id: string;
  site_id: string;
  version_id: string;
  access_mode: "public" | "password" | "allowlist" | "org_only";
  schema_version: number; // accepts 1 AND 2; fails closed outside that range
  expires_at?: string; // v2+, RFC3339; past → 410 "link expired" at the edge
}
```

`schema_version` **2** (Phase 2) added the optional `expires_at`; the Worker
parses both v1 and v2 (a v1 value has no `expires_at` and never expires).

It is owned by the repo-root `contracts/` package (JSON Schema → Go struct + TS
type + CI round-trip test) and published as **`@dropway/contracts`**.
`src/route.ts` imports `KVRouteValue` / `SCHEMA_VERSION` / `safeParseKVRouteValue`
from it and re-exports them under the Worker's local `RouteValue` /
`SUPPORTED_SCHEMA_VERSION` names. Untrusted KV values are validated by
`parseRouteValue()` (rejecting bad shapes, non-UUID ids, and unknown
`schema_version`). The package is a workspace dependency; Wrangler bundles it at
deploy, and `tsconfig.json` / `vitest.config.ts` alias it to its source so
type-check and tests resolve it without a build step.

## Bindings (`wrangler.toml`)

| Binding  | Type | Purpose                                                                       |
| -------- | ---- | ---------------------------------------------------------------------------- |
| `ROUTES` | KV   | `route:<host>` → `RouteValue` routing projection (read-only). Also the **default** denylist namespace (`revoked:*` keys) when `REVOKED` is unset. |
| `BUCKET` | R2   | Single private bucket: `manifests/<org>/<site>/<version>.json` + `blobs/<org>/<sha256>`. |
| `LIMITS` | KV   | **(Phase 4, optional)** rate-limiter counters (`rl:*`) + per-org status (`org_status:<org>`). Absent → rate limiting no-op + status check skipped. |
| `REVOKED`| KV   | **(Phase 4, optional)** hard-revocation denylist (`revoked:user\|site\|org:*`). Absent → reuse `ROUTES` with the `revoked:` prefix (revocation contract default). |

### Vars (`wrangler.toml [vars]`)

| Var             | Purpose                                                                 | Default                                          |
| --------------- | ----------------------------------------------------------------------- | ------------------------------------------------ |
| `EDGE_JWKS_URL` | Edge signer public JWKS (OKP/Ed25519), fetched + cached to verify tokens. | `https://api.dropway.dev/.well-known/edge-jwks` |
| `APP_AUTHZ_URL` | Dashboard `/authz` exchange a gated request 302s to.                    | `https://app.dropway.dev/authz`                  |
| `RATE_LIMIT_MAX` | **(Phase 4)** max requests per window per identity.                    | `600`                                            |
| `RATE_LIMIT_WINDOW_SECONDS` | **(Phase 4)** rate-limit window length (seconds).          | `60`                                             |

Both have safe production defaults in `src/config.ts`; set them per environment.
There are **no secrets** here: the Worker only ever verifies with the *public*
JWKS; the edge signing key lives in the Go API (`EDGE_SIGNING_KEY`, see
`deploy/.env.example`).

The namespace/bucket IDs in `wrangler.toml` are **placeholders**: the infra
agent fills them in per environment (or via `wrangler kv namespace create` /
`wrangler r2 bucket create`). The production `[[routes]]` block is committed but
commented out until `dropwaycontent.com` is live on Cloudflare.

## Layout

```
edge/serving-worker/
├── src/
│   ├── index.ts     # fetch handler + serve(): rate limit, KV lookup, expiry, org-status, dispatch, blob stream, Cache API, 404/410/429/503 + SW block
│   ├── route.ts     # PURE host normalize + route parse (via @dropway/contracts) + path sanitize + isRouteExpired
│   ├── manifest.ts  # PURE manifest model + parse + path→entry resolution; manifest/blob R2 keys
│   ├── http.ts      # PURE Content-Type + Cache-Control; re-exports content security headers
│   ├── security.ts  # PURE content/platform CSP + COOP/CORP/nosniff/no-referrer/frame + service-worker-script block (Phase 4)
│   ├── ratelimit.ts # PURE fixed-window KV-counter rate limiter + per-org status read (Phase 4)
│   ├── revoke.ts    # PURE hard-revocation denylist (revoked:user|site|org → min_iat) check (Phase 4)
│   ├── config.ts    # gated-path config (EDGE_JWKS_URL/APP_AUTHZ_URL), issuer + cookie name
│   ├── edgetoken.ts # edge-token verification (jose): JWKS fetch+cache, alg/iss/aud/exp/site_id checks; surfaces iat
│   ├── authz.ts     # cookie read, /authz 302, __Host-edge Set-Cookie, /__authz/callback, safe-next redirect
│   └── gated.ts     # gated dispatch: verify cookie → revocation check → serve private, or 302; callback handling
├── test/
│   └── serve.test.ts  # vitest: public + gated + platform headers, SW block, rate limit/429, org suspension, revocation min_iat (mocked KV/R2/JWKS/clock)
├── wrangler.toml
├── vitest.config.ts   # node pool; aliases @dropway/contracts → source
├── tsconfig.json      # extends ../../tsconfig.base.json + workers types; @dropway/contracts path
├── package.json       # @dropway/serving-worker
└── README.md
```

The routing/HTTP logic in `route.ts` and `http.ts` is intentionally pure so it
is unit-testable without a live edge; `index.ts` is a thin shell that wires the
injected `ROUTES`/`BUCKET` bindings to those functions.

## Develop

> Run `pnpm install` at the repo root first (this package does **not** install
> on its own). Commands below assume deps are present.

```sh
pnpm --filter @dropway/serving-worker test       # vitest (mocked KV + R2)
pnpm --filter @dropway/serving-worker typecheck  # tsc --noEmit
pnpm --filter @dropway/serving-worker dev         # wrangler dev (needs binding IDs)
pnpm --filter @dropway/serving-worker deploy      # wrangler deploy
```

For `wrangler dev`, point the bindings at preview resources and seed a route, a
deploy manifest, and the blob(s) it references (ids must be real UUIDs, since the
contract validator fails closed otherwise):

```sh
ORG=11111111-1111-1111-1111-111111111111
SITE=22222222-2222-2222-2222-222222222222
VER=33333333-3333-3333-3333-333333333333
SHA=$(shasum -a 256 index.html | cut -d' ' -f1)

wrangler kv key put --binding=ROUTES --preview \
  "route:acme.localhost" \
  "{\"org_id\":\"$ORG\",\"site_id\":\"$SITE\",\"version_id\":\"$VER\",\"access_mode\":\"public\",\"schema_version\":1}"

# Manifest: request path → { sha256, content_type }
echo "{\"schema_version\":1,\"files\":{\"index.html\":{\"sha256\":\"$SHA\",\"content_type\":\"text/html; charset=utf-8\"}}}" \
  | wrangler r2 object put --preview \
      "dropway-content-preview/manifests/$ORG/$SITE/$VER.json" --pipe

# Content-addressed blob
wrangler r2 object put --preview \
  "dropway-content-preview/blobs/$ORG/$SHA" --file=./index.html
```

## Phase boundaries

- **Phase 1:** `public` only. JWT-free, cacheable.
- **Phase 2:** `password` (host-scoped cookie, anon identity) and
  `allowlist` / `org_only` (302 → `app.dropway.dev/authz` host-scoped token
  exchange; the Worker verifies *that* edge token, never the operator JWT) plus
  **edge link-expiry** (`expires_at` in the v2 `RouteValue` → `410`). Gated
  responses are always `private, no-store` and never enter the public Cache API.
- **Phase 4 (here):** edge security/ops hardening, covering content-security headers on
  every response (content vs strict platform CSP, COOP/CORP/nosniff/no-referrer/
  frame), **service-worker registration block** on content origins, **edge rate
  limiting** (`429` + `Retry-After`) + **denial-of-wallet** per-org suspension
  (`503` block page), and **hard revocation** via the KV `min_iat` denylist on
  the gated path (immediate ban/unshare; fail-closed, rebuildable). See **Edge
  hardening (Phase 4)** above. Out of scope this phase (Go/infra-owned backlog):
  SSO/SAML, SCIM, runtime collection-DB + LLM/image proxy APIs, third-party
  malware-scanning vendor integration.

## License

`FSL-1.1-Apache-2.0` (core / FSL boundary, see repo-root `LICENSE`).
