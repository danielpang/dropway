<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# @shipped/serving-worker

Cloudflare Worker that serves tenant static sites on `*.shippedusercontent.com`
(the PSL-listed content domain — never a subdomain of the app/auth domain).
This is the **Phase-1 PUBLIC serve path**: the 95% case that is **JWT-free and
cacheable** (see `docs/ARCHITECTURE.md` §3 and §6).

The Worker is a **thin router and a read-only consumer** of its bindings. The
Go API (`api.shipped.app`) is the real authz boundary and the **sole writer** of
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
  → access_mode === "password" | "allowlist" | "org_only":
        501 Phase-2 STUB ("/authz exchange") — NOT implemented here.
```

The public path **never reads a JWT** — any `Authorization` header is ignored.

### Path resolution rules

- Root (`/`) and directory paths (`/blog/`) → `…/index.html`.
- Explicit asset paths (`/assets/app.css`) → served directly.
- Extension-less "pretty" paths (`/about`) → try, in order:
  `about`, `about/index.html`, `about.html`.
- Path traversal (`..`, encoded `..`, NUL, backslash, malformed `%`-encoding)
  is rejected and **fails closed to a 404** — a request can never escape its
  version prefix into another org/site/version.

### Cache policy (`src/http.ts`)

| Asset class                                  | `Cache-Control`                          |
| -------------------------------------------- | ---------------------------------------- |
| Content-hash fingerprinted (`app.4f3a9c2b.js`) | `public, max-age=31536000, immutable`    |
| HTML / non-hashed assets                     | `public, max-age=60, must-revalidate`    |
| Gated (Phase-2 stub) responses               | `private, no-store` (never shared-cached) |

Every public response also carries defense-in-depth security headers
(`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`X-Frame-Options: SAMEORIGIN`). CSP is **not** the isolation control here —
domain/PSL separation is (§10).

## The cross-language contract

The KV value at `route:<host>` is the one genuine Go↔TS data contract:

```ts
interface RouteValue {
  org_id: string;
  site_id: string;
  version_id: string;
  access_mode: "public" | "password" | "allowlist" | "org_only";
  schema_version: number; // pinned; the Worker fails closed on a mismatch
}
```

It is owned by the repo-root `contracts/` package (JSON Schema → Go struct + TS
type + CI round-trip test) and published as **`@shipped/contracts`**.
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
| `ROUTES` | KV   | `route:<host>` → `RouteValue` routing projection (read-only).                |
| `BUCKET` | R2   | Single private bucket: `manifests/<org>/<site>/<version>.json` + `blobs/<org>/<sha256>`. |

The namespace/bucket IDs in `wrangler.toml` are **placeholders** — the infra
agent fills them in per environment (or via `wrangler kv namespace create` /
`wrangler r2 bucket create`). The production `[[routes]]` block is committed but
commented out until `shippedusercontent.com` is live on Cloudflare.

## Layout

```
edge/serving-worker/
├── src/
│   ├── index.ts     # fetch handler + serve(): KV lookup, dispatch, manifest fetch, blob stream, Cache API, 404, Phase-2 stub
│   ├── route.ts     # PURE host normalize + route parse (via @shipped/contracts) + path sanitize
│   ├── manifest.ts  # PURE manifest model + parse + path→entry resolution; manifest/blob R2 keys
│   ├── http.ts      # PURE Content-Type + Cache-Control + security headers
│   └── (RouteValue now comes from @shipped/contracts — no local mirror)
├── test/
│   └── serve.test.ts  # vitest: routing, manifest resolution, blob fetch, Cache API (mocked KV + R2)
├── wrangler.toml
├── vitest.config.ts   # node pool; aliases @shipped/contracts → source
├── tsconfig.json      # extends ../../tsconfig.base.json + workers types; @shipped/contracts path
├── package.json       # @shipped/serving-worker
└── README.md
```

The routing/HTTP logic in `route.ts` and `http.ts` is intentionally pure so it
is unit-testable without a live edge; `index.ts` is a thin shell that wires the
injected `ROUTES`/`BUCKET` bindings to those functions.

## Develop

> Run `pnpm install` at the repo root first (this package does **not** install
> on its own). Commands below assume deps are present.

```sh
pnpm --filter @shipped/serving-worker test       # vitest (mocked KV + R2)
pnpm --filter @shipped/serving-worker typecheck  # tsc --noEmit
pnpm --filter @shipped/serving-worker dev         # wrangler dev (needs binding IDs)
pnpm --filter @shipped/serving-worker deploy      # wrangler deploy
```

For `wrangler dev`, point the bindings at preview resources and seed a route, a
deploy manifest, and the blob(s) it references (ids must be real UUIDs — the
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
      "shipped-content-preview/manifests/$ORG/$SITE/$VER.json" --pipe

# Content-addressed blob
wrangler r2 object put --preview \
  "shipped-content-preview/blobs/$ORG/$SHA" --file=./index.html
```

## Phase boundaries

- **Phase 1 (here):** `public` only. JWT-free, cacheable.
- **Phase 2 (stubbed):** `password` (host-scoped cookie, no identity) and
  `allowlist` / `org_only` (302 → `app.shipped.app/authz` host-scoped token
  exchange; the Worker verifies *that* token, never the operator JWT). The
  current `gatedStub` returns a clearly-marked `501` and does **no** identity
  work — see the `TODO(phase-2)` in `src/index.ts`.

## License

`FSL-1.1-Apache-2.0` (core / FSL boundary — see repo-root `LICENSE`).
