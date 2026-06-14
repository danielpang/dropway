<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Test coverage spec

Per-component **unit** coverage targets and the **achieved** numbers, plus how
each component's behavior is actually verified. Measured with:

```sh
go test -cover ./...                                   # OSS unit
go test -tags cloud -cover ./cloud/... ./services/...  # cloud unit
go test ./... -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | tail -1   # overall
pnpm -r test                                           # TS (dashboard + worker)
```

> **What "full coverage" means here.** The raw module total (**54.7%**) is *not*
> the headline — it averages in `main()`/wiring and sqlc-generated code that
> aren't meaningfully unit-testable. We hold each component to a target keyed to
> what it *is*: **pure logic → high unit target**; **DB/external-bound code →
> low unit target + full coverage by the docker integration suite**
> (`-tags integration` / `-tags 'cloud integration'`); **wiring/generated →
> excluded** (built + integration-exercised, never unit-tested).

## Pure logic — target ≥ 90% unit (the security/business surface)

| Component | Target | Achieved | Notes |
|---|---|---|---|
| `internal/quota` | ≥ 90% | **100%** | pure cap policy |
| `internal/manifest` | ≥ 90% | **100%** | deploy digest |
| `internal/logx` | ≥ 90% | **100%** | request-id sanitize/propagate |
| `internal/edgerevoke` | ≥ 90% | **100%** | revocation denylist (fail-closed) |
| `services/api/internal/config` | ≥ 90% | **100%** | env parse + iss/aud guard |
| `services/api/internal/router` | ≥ 70% | **100%** | route/authz-boundary wiring |
| `internal/customdomains` | ≥ 90% | **97.5%** | CF-for-SaaS state machine + fake |
| `internal/middleware` | ≥ 90% | **97.3%** | auth + RLS `SET LOCAL` |
| `internal/pwhash` | ≥ 90% | **95.0%** | bcrypt + constant-time dummy |
| `internal/httpx` | ≥ 90% | **95.0%** | error→status, 402 body |
| `internal/edgetoken` | ≥ 90% | **91.0%** | edge-token mint/verify (alg-pinned) |
| `internal/projection` | ≥ 90% | **90.2%** | RouteValue + KV/local writers |
| `cloud/quota` | ≥ 95% | **100%** | cloud hard-cap bands |
| `internal/auth` | ≥ 90% | **81.0%** | residual = JWKS-fetch error branches (integration-exercised); iss/aud enforcement now guarded in `config` |

## HTTP handlers — target ≥ 80% unit (+ integration)

| Component | Target | Achieved | Notes |
|---|---|---|---|
| `services/api/internal/handlers` | ≥ 80% | **76.2%** | table-driven over status/authz with the fake store; the DB-touching arms are integration-covered |
| `cloud/billing` | ≥ 80% | **61.4%** | webhook dedupe/apply, price→tier, signature parse, owner/admin gate, store logic — all unit-tested; the **real Stripe network client** is integration/manual, not faked |

## DB / external-bound — low unit % BY DESIGN, covered by integration

These connect to Postgres or S3/R2; their behavior is proven by the **docker
integration suite** (RLS isolation, deploy→publish, billing lifecycle,
revocation, GC, DR rebuild, and the dedicated **RLS policy suite** over all 10
tenant tables). Unit tests cover only the extractable pure logic.

| Component | Unit target | Achieved (unit) | Integration |
|---|---|---|---|
| `services/api/internal/store` | ≥ 15% pure | **15.7%** | ✅ full (RLS + all CRUD + the RLS policy suite) |
| `services/api/ops` | ≥ 15% | **15.6%** | ✅ DR rebuild + GC |
| `internal/storage` | ≥ 90% pure | **52.5%** | ✅ S3/MinIO blob+manifest paths (the 0% lines are live-S3 SDK calls) |

## CLI — target ≥ 80% unit

| Component | Target | Achieved |
|---|---|---|
| `cli/internal/cmd` | ≥ 80% | **95.2%** |
| `cli/internal/manifest` | ≥ 80% | **90.2%** |
| `cli/internal/api` | ≥ 80% | **82.5%** |

## TypeScript

| Component | Target | Achieved |
|---|---|---|
| `edge/serving-worker` | ≥ 90% branch | **174 tests** (security headers, rate-limit, revocation fail-closed, manifest/serve) — % not measured¹ |
| `apps/dashboard` (pure `lib/`) | best-effort | **55 tests** (402 narrowing, `/authz` open-redirect, billing helpers, audit matcher) |
| `contracts` | typecheck | round-trip test in `internal/projection` + worker |

¹ Coverage % isn't reported because `@vitest/coverage-v8` isn't a declared dep
(installing one was out of scope for the coverage run). To enable: add
`@vitest/coverage-v8@4.1.8` to the worker + dashboard devDeps and run
`vitest run --coverage`.

## Excluded (no unit target)

`services/api/cmd/api`, `cli/cmd/shipped` (process `main()`/wiring — exercised by
build + the integration suite), `services/api/internal/store/db` (sqlc-generated),
`ee` (empty stub), and `apps/dashboard` React/RSC components (no DOM test runner;
behavior covered by typecheck + the Go integration of the APIs they call).

## Recommended CI gate

A simple per-package floor can be enforced in CI (fail if a package drops below
its target) — e.g. parse `go test -cover ./...` and compare against the targets
above. Wire it into `.github/workflows/ci.yml`'s `go` job before treating these
numbers as a contract.
