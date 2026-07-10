<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway for macOS — native dashboard app scope (Vercel Native SDK)

Proposal for building a macOS-native desktop app with full feature parity to
the web dashboard, on Vercel's [Native SDK](https://native-sdk.dev)
([vercel-labs/native](https://github.com/vercel-labs/native), Apache-2.0,
evaluated at **v0.4.2**). Status: **scoping draft for team review** — no app
code exists yet. Estimates are in engineer-weeks (ew).

## 1. Summary and recommendation

The Native SDK renders real native windows with its own engine: views are
declarative `.native` markup, logic is plain **Zig**, and there is no browser,
WebView, or JS runtime in the binary. macOS is its mature platform (Metal,
native menus/dialogs/tray, documented signing/notarization/DMG); iOS/Android
are experimental. It is pre-1.0 and moving fast — v0.4.0, two months before
this writing, was a wholesale architecture pivot.

Two SDK gaps are decisive for our app shape, and both happen to be exactly
what the Dropway CLI already implements in Go:

1. **No OAuth redirect receipt.** Custom URL schemes are packaging metadata
   only (no url-opened event exists), and there is no loopback-server
   primitive or thread→Msg injection API. The sanctioned escape hatch for
   anything long-running is `fx.spawn` (subprocess with line-streamed stdout).
2. **File transfer is impossible in-framework.** `fx.fetch` caps request
   bodies at 64 KiB and responses at 256 KiB (silently truncated); file
   effects cap at 1 MiB. Site deploys and skill uploads/downloads cannot ride
   these.

**Recommendation:** build the app as a **thin Zig/`.native` UI over a bundled
Go sidecar** (`dropway-agent`) derived from `cli/internal/*`. The sidecar owns
all authentication, token refresh, and **all** API traffic; the Zig app is a
pure Elm-style view/state layer that never sees a token or a raw HTTP
response.

- **Total scope: ≈ 60–80 ew (6–9 calendar months with 2 engineers)**, in five
  milestones with a hard **go/no-go gate after a 4-week spike** (M0).
- The **WebView-shell fallback** (§9a — same SDK hosting the existing Next
  dashboard, ~6–10 ew) should be pre-agreed as the bail-out if M0 fails its
  exit criteria.

## 2. Why Dropway is unusually well-positioned

- The backend is already API-first: the Go API is the authz boundary, with a
  full OpenAPI contract (`services/api/openapi/openapi.yaml`) and short-lived
  (10-minute) EdDSA JWTs.
- A complete **OAuth 2.1 authorization server** already runs inside the
  dashboard (Better Auth + oauth-provider): RFC 7591 dynamic client
  registration, PKCE, refresh tokens via `offline_access`, `.well-known`
  metadata. The CLI and MCP clients exercise it daily.
- The CLI (`cli/internal/`) already implements the entire native-client flow
  in ~1,300 LOC of core logic (~4.6k with tests and skills support):
  - `auth/oauth.go` — RFC 9728 discovery → DCR → loopback PKCE → token
    exchange and refresh (`auth.Token()` handles expiry slack and
    refresh-token rotation). The `localhost`-hostname-not-`127.0.0.1` redirect
    quirk (`oauth.go:78`) is load-bearing against Better Auth.
  - `manifest/manifest.go` — folder walk + sha256 manifest, digest byte-exact
    with the web `lib/deploy.ts` flow.
  - `cmd/deploy.go` + `api/api.go` — prepare → presigned PUT direct to
    R2/S3 → finalize → publish.
  - `cmd/skills.go` — skills push/pull/list.

A native app is therefore overwhelmingly a **client build**; the server-side
enabling work is small (§5).

## 3. Architecture

### 3.1 The sidecar proxies ALL API traffic

The Zig app performs **zero** `fx.fetch` calls to the Dropway API. We
considered the split model (Zig fetches JSON reads, sidecar handles only
auth + transfer) and rejected it:

1. **The 256 KiB response cap is not a corner case.** Every list endpoint
   except `/v1/audit` is unpaginated today — `GET /v1/sites`,
   `/v1/sites/{id}/versions`, `/v1/feed`, `/v1/sites/{id}/comments`,
   `/v1/skills` all return full arrays. A busy org will cross 256 KiB and the
   response arrives *silently truncated*, corrupting JSON parsing. Fixing that
   in-framework means paginating a dozen Go endpoints — more server work than
   the sidecar costs.
2. **10-minute access tokens make split auth ugly.** With `fx.fetch` the Zig
   app must hold tokens, detect expiry/401, ask the sidecar to refresh, and
   retry — a state machine duplicated across every request site, in a language
   the team is learning. In the proxy model, refresh lives in one place: the
   existing `auth.Token()`.
3. **TLS maturity.** The SDK's HTTP rides Zig `std.http` and its young TLS
   stack; Go `net/http` handles corporate proxies, custom CAs, and HTTP/2
   today. Routing everything through Go deletes an entire risk class.
4. **Testability.** The sidecar returns *UI-shaped view models* (trimmed,
   paginated, pre-sorted), keeping Zig models small; the API contract stays
   covered by Go unit tests reusing the CLI's fakes.

### 3.2 Process model: per-invocation spawns, CLI-style

Rather than a daemon with hand-rolled IPC framing, `dropway-agent` (a new Go
binary in this monorepo sharing `cli/internal/*`) runs **one process per
request**:

```
dropway-agent request --method GET --path /v1/sites --out <scratch-file>
dropway-agent request --method POST --path /v1/sites/{id}/comments --body-file <f> --out <f>
dropway-agent login                                  # long-lived: loopback OAuth, streams progress
dropway-agent deploy --site <id> --dir <dropped-path> # streams hash/upload/finalize progress
dropway-agent skill-pull --id <id> --dest <dir>
dropway-agent skill-push --id <id> --dir <dir>
dropway-agent analytics-flush
dropway-agent update-check | update-install
```

- **Framing:** line-delimited JSON events (`{"ev":"progress",…}`,
  `{"ev":"done","status":200,…}`). Responses under ~3.5 KiB inline in the
  `done` line (the spawn line cap is 4 KiB); larger view models go to a
  scratch file named in the `done` line, read via file effects (≤ 1 MiB — the
  sidecar guarantees paged/trimmed view models under this).
- **Never put secrets or bodies on argv** (visible in `ps`): tokens come from
  the credential store, request bodies via `--body-file`.
- **Concurrency:** the SDK caps 16 in-flight effects; low-priority polls
  (domain status, feed refresh) are serialized behind a small effect queue in
  the update loop.
- **Latency:** a static Go binary cold-starts in ~10–30 ms; fine for a
  request-per-interaction UI with skeleton states. Spawn round-trip latency is
  an M0 exit criterion; if it fails, a daemon variant sits behind the
  identical command surface (contained change).

### 3.3 Tokens

The **sidecar owns storage and refresh**. Replace the CLI's plaintext
`credentials.json` (`cli/internal/auth/store.go`) with a Keychain-backed store
(go-keychain or `/usr/bin/security`), keeping the file store as a dev/CI
fallback behind `--cred-store=file`. The SDK's runtime credentials API is
deliberately *not* used for OAuth tokens — that would put the Zig app in the
token path and create two owners of one secret. Only Go code ever touches
`access_token`/`refresh_token`; the Zig app learns `signed_in: bool` + profile
from `dropway-agent whoami`. A file lock guards refresh, since per-invocation
refresh across parallel spawns can race refresh-token rotation.

### 3.4 OAuth login flow

Exactly the CLI flow, lifted from `cli/internal/auth/oauth.go`: discovery →
DCR (client_name "Dropway for macOS") → loopback listener on `127.0.0.1:0`
with `http://localhost:{port}/callback` → PKCE → token exchange. The Zig app
spawns `dropway-agent login`, which emits the authorize URL as a progress
line; the app opens it via `runtime.openExternalUrl` (allowlisted origin) and
shows a "waiting for browser" state until the spawn's `done` line. The
existing branded loopback landing page is reused. Sign-up and OAuth consent
remain web (§6).

### 3.5 What runs where

| Concern | Zig/.native app | Go sidecar | Server (new) |
|---|---|---|---|
| Views, state, navigation, drag-drop receipt, dialogs, tray, notifications | ✅ | | |
| OAuth login/refresh, Keychain | | ✅ | |
| All REST calls (view-model shaping, paging/trim) | | ✅ | |
| Folder hash + presigned uploads, skill pull/push | | ✅ (reuse `cli/internal`) | |
| Member/invite/role mutations | | ✅ (calls bridge) | ✅ bearer bridge (§5) |
| PostHog analytics | event spool file | ✅ flush (posthog-go) | |
| Auto-update | prompt UI | ✅ check/verify/install | update feed hosting |

## 4. Feature-parity mapping

Sidecar column: `req` = proxied request(s); `stream` = long-lived spawn with
progress lines. Flags: ⚠ = awkward/lossy vs web, 🔴 = needs server work or
stays web.

| Screen | Native SDK components | API endpoints | Sidecar | Flags |
|---|---|---|---|---|
| /dashboard — sites list + create | table/card grid, dialog, skeleton | GET/POST `/v1/sites`, GET `/v1/me` | req | unpaginated list → sidecar pages it |
| /sites/[id] — deploy, versions, rollback, feed, comments | `files_dropped` dropzone (dir paths, 32-path cap/event), progress, table/timeline, textarea + list, switch | prepare/deployments/publish, versions, feed(-meta), vote, comments | **stream** (deploy), req rest | deploy progress via `on_line`; comments poll as web does |
| /sites/[id]/settings — access, allowlist | radio/segmented, list + input, confirm dialog | access, allowlist CRUD | req | |
| /sites/[id]/domains — DCV, status | form, table, copy (clipboard), badge; poll timer | domains CRUD, `/v1/domains/{id}/status` | req (polled) | poll serialized in effect queue |
| /feed — org feed, votes, comments | virtual list (sidecar pages), cards, markdown | GET `/v1/feed`, per-item vote/comments | req | ⚠ no remote images in markdown/cards (web feed has none either) |
| /skills — browser + folders | list/table, tree, combobox | `/v1/skills`, `/v1/skill-folders*` | req | |
| /skills/new, /skills/[id]/edit | **textarea** (web parity — web editor is a plain textarea), file rows, open dialog | create/update, uploads prepare+finalize | **stream** (upload) | ⚠ no syntax highlighting; preview OK minus images |
| /skills/[id] — detail, download | markdown, table, save dialog | detail, files, download, feed/vote/comments | **stream** (download is inline JSON ≤ 5 MiB → sidecar writes to disk) | exceeds 256 KiB cap — sidecar-only |
| /members — roles, invites, storage, revoke | table, form, dialog, meter | `/v1/members`, `/v1/storage`, preflight, revoke | req + **bridge** | 🔴 invite/role/remove are Better Auth cookie-only today (§5) |
| /audit — admin | table, pagination | GET `/v1/audit?limit&offset` | req | already paginated |
| /billing (cloud-only) | cards, plan table, buttons → browser | billing, checkout, portal | req | checkout/portal via `openExternalUrl`; return detected by polling `/v1/billing` (no deep-link-back exists in the SDK) |
| /settings — org toggles, MCP | switches, copy block | policy, allow-external-sharing, mcp | req | |
| /mcp, /cli docs | markdown (bundled) | — | — | ⚠ no images; link out for visuals |
| onboarding | native first-run: sign-in → org check → first deploy | `/v1/me` etc. | req | rebuilt native, simplified |
| sign-in / sign-up | "Sign in with browser" button | OAuth | **stream** (`login`) | 🔴 sign-**up** + org creation stay web |
| accept-invitation, /oauth/consent | — | — | — | 🔴 stay web permanently |

Images: the SDK registers at most 16 images (≤ 1 MiB each, no remote `src`).
The dashboard has no remote avatars today (initials-style rendering), so app
icon + a few static illustrations fit trivially; any future remote imagery is
fetched to disk by the sidecar and registered — deferred.

## 5. Server-side enabling workstream (small, mandatory)

1. **Members bearer bridge (1–1.5 ew).** Better Auth org mutations
   (`inviteMember`, `updateMemberRole`, `removeMember`, `cancelInvitation`,
   `listInvitations`) only accept the Next app's cookie session
   (`openapi.yaml:155`: "API only READS roles"). Add thin Next.js route
   handlers (e.g. `/api/native/members/*`) that validate the OAuth access
   token — the authorization server lives in the same app and can verify its
   own EdDSA tokens — and call `auth.api.*` server-side with the same role
   rules. Moving these mutations into the Go API was rejected (violates the
   stated ownership boundary); a WebView-hosted /members was rejected (second
   cookie sign-in inside the app).
2. **Optional additive pagination** on `/v1/feed`, `/v1/sites`, `/v1/skills`,
   `/v1/sites/{id}/versions` (0.5–1 ew). Not required — the sidecar shields
   the UI — but bounds sidecar memory for pathological orgs and benefits web.
3. **Update feed hosting** (part of M4): static signed manifest per the SDK's
   feed format, published from release CI.

## 6. What stays web permanently

- **Sign-up + org creation** (Better Auth email flows; the app links out,
  the user returns and hits "Sign in").
- **OAuth consent** (`app/oauth/consent`) — it *is* the browser leg of the
  app's own login.
- **accept-invitation** (email link → browser by nature).
- **Stripe Checkout/Portal** (Stripe-hosted; the invariant that entitlements
  are written solely by the webhook is untouched).
- Password-gated **site viewing** (visitor-facing, not dashboard).

## 7. Milestones

**M0 — de-risking spike (4 wks, 1 senior eng; go/no-go gate).** Toolchain +
hello-world app; `dropway-agent` skeleton; end-to-end OAuth login via spawn +
Keychain; **drag a folder onto the window → deployed site live**; spawn
latency measurement; `native package` → signed, notarized DMG on a clean Mac;
virtual list with 1k sidecar-paged rows.
*Exit criteria:* login + deploy reliable; spawn round-trip < 150 ms p95;
notarized DMG launches cleanly; no framework blocker (e.g. `files_dropped`
delivers directory paths as documented). **Any failure → adopt fallback §9a.**

**M1 — sites core (6 wks).** App shell (nav, org header, error/skeleton
patterns — the design-system mapping everything else reuses), /dashboard,
/sites/[id] complete (deploy stream UI, versions/rollback, comments, feed
toggle), site settings, sign-in polish. *Internal dogfood build.*

**M2 — feed + skills (6 wks).** Feed with votes/comments (site + skill
variants), skills browser + folders, detail with download-to-disk,
create/edit with textarea editor + upload streams.

**M3 — org/admin + money (5 wks).** Members (incl. the §5 bridge), audit,
settings, domains (DCV + polling), billing hand-off, docs pages, onboarding.
*Feature-parity beta.*

**M4 — distribution + polish (5 wks).** Auto-update, tray/menu-bar
quick-deploy (drag onto the tray icon — the marquee native win),
notifications (deploy done, domain verified, invite accepted), PostHog
wiring, accessibility, perf, crash reporting, release CI, public beta → GA.

## 8. Monorepo, CI, distribution, testing

- **Placement:** `apps/native/` (fits the `apps/*` workspace glob) with
  `apps/native/agent/` for the Go sidecar, importing `cli/internal/*` (may
  require promoting a few packages out of `internal/`, or nesting the agent
  under `cli/` — decide in M0; no logic changes either way). The Zig/`native`
  build is wrapped in a `package.json` script so `pnpm build` stays the single
  entry point.
- **CI:** add a macOS runner job (`native check`/`native test` + Go agent
  tests); release workflow gains Developer ID signing + notarytool + DMG +
  update-feed publish. **Pin and vendor the SDK at v0.4.2** (subtree or
  hash-checked tarball) so pre-1.0 churn can't break CI.
- **Auto-update:** the SDK defines only a signed (Ed25519) feed format —
  application is DIY. Sparkle-style flow in the sidecar: `update-check` on
  launch/daily → notify → `update-install` downloads, verifies, swaps the
  `.app` on quit + relaunch. Embedding Sparkle itself (ObjC framework in a Zig
  app) was rejected as uncharted. ~2 ew inside M4.
- **Analytics:** the UI appends events to a local spool file; the sidecar
  ships batches via `posthog-go` (no PostHog Zig SDK exists).
- **Testing:**
  - Sidecar: Go unit tests reusing the CLI's fakes; golden tests for
    view-model shapes and line framing (adversarial sizes around the 4 KiB
    line cap).
  - Zig: `native test` with the SDK's fake effects executor — each screen's
    update loop against canned sidecar `done`/`progress`/error lines;
    headless `native check` snapshots.
  - Contract: CI diffs the OpenAPI spec hash and fails when the spec changes
    without a sidecar view-model update (mirrors `lib/api-generated`).
  - E2E nightly on the macOS runner against the local dev stack: login,
    deploy a fixture folder, rollback, skill push/pull. Manual matrix per
    release: clean-machine install, corporate proxy, Keychain prompts,
    Gatekeeper.

## 9. Risk register

| Risk | L | I | Mitigation |
|---|---|---|---|
| Pre-1.0 SDK churn (v0.4.0 pivoted the architecture 2 months ago; no production case studies) | High | High | Pin + vendor v0.4.2; upgrade only at milestone boundaries; keep business logic in Go (survives a UI-layer rewrite); fallback §9a pre-agreed |
| SDK missing primitive discovered mid-build | Med | High | M0 exercises every load-bearing primitive; WebView escape hatch for any single unbuildable screen |
| Zig skill acquisition (team is TS/Go) | High | Med | One senior owns Zig from M0; sidecar-max design keeps the Zig surface small and mostly declarative |
| 256 KiB response / 64 KiB request caps | — | — | Eliminated by design: zero `fx.fetch` to the API; sidecar bounds view models |
| 4 KiB spawn-line cap corrupting payloads | Med | Med | Scratch-file handoff from day one; framing unit-tested |
| Zig std.http TLS / corporate proxies | — | — | Moot: all TLS in Go |
| Concurrent refresh-token rotation across spawns | Med | Med | File lock around refresh; retry-once-on-401 |
| Members bridge slips (web-team dependency) | Med | Med | Blocks only part of M3; interim: read-only members + "Manage in browser" |
| No auto-update → stale clients vs evolving API | Med | Med | M4 updater; agent sends `X-Client-Version`; graceful minimum-version advisory |
| Spawn-per-request latency feels sluggish | Low–Med | Med | Measured at M0 (exit criterion); daemon fallback behind identical command surface |
| Notarization/signing friction in CI | Med | Low | Documented SDK path; set up in M0, not M4 |
| Parity drift while web keeps shipping for 6–9 months | High | Med | Living parity table; OpenAPI-hash CI check; scope targets parity as of a cut date with drift buffer |

## 10. Alternatives considered

**(a) WebView-shell mode of the same SDK — recommended fallback.** The SDK's
older, more proven half: host the existing Next dashboard in first-class
WKWebView children (remote `allowed_origins` or static export via
`zero://app`), adding native tray, folder drag-and-drop (bridge command into
the existing `lib/deploy.ts` upload path), Keychain-assisted session, and
notifications. ~**6–10 ew**, near-zero parity risk, automatic parity with
future web features. Costs: it *is* the web app (feel/perf), cookie auth
inside a shell, less native payoff. **Bail to (a) if:** the M0 gate fails, M1
velocity implies > 90 ew total, or the SDK ships another breaking pivot
mid-build.

**(b) Tauri / Electron.** Reuse the React dashboard nearly verbatim with
mature ecosystems (auto-update, deep links, IPC); ~12–20 ew. Rejected as
primary because the goal is evaluating the Native SDK and true native UI;
Electron's footprint and Tauri's WebKit quirks re-create most of (a)'s
downsides on a new toolchain.

**(c) Status quo (responsive web + CLI).** Zero cost; drag-and-drop deploy
from Finder, tray presence, and OS integration stay unavailable; the CLI
already serves power users. The honest baseline this project must beat.

## 11. Effort summary

Assumes 2 engineers: one senior learning Zig, one Go/full-stack.

| Workstream | ew (base) |
|---|---|
| M0 spike | 4 |
| Sidecar (auth/Keychain, proxy + view models, deploy/skills streams, analytics, updater) | 7–9 |
| Zig app foundation (shell, nav, design-system mapping, effects/spawn plumbing, error patterns) | 5–6 |
| Screens: sites core 6 · feed 2.5 · skills 6 · members + audit 3.5 · settings/domains/billing 4.5 · onboarding/sign-in/docs 2.5 | 25 |
| Server bridge + optional pagination | 1.5–2.5 |
| Packaging, signing, CI, auto-update, release eng | 4–5 |
| QA, E2E harness, beta hardening, parity-drift catch-up | 5–7 |
| **Subtotal** | **52–59** |
| Pre-1.0 uncertainty multiplier ×1.15–1.35 (SDK churn, Zig ramp, unknown-unknowns) | — |
| **Total** | **≈ 60–80 ew · 6–9 months calendar** |

The estimate is deliberately not optimistic: "rewrite a 24k-LOC dashboard in a
UI toolkit the team doesn't know, on a pre-1.0 framework" is a reference class
that routinely doubles naive estimates. The multiplier and the M0 gate are the
honesty mechanisms.

## 12. Key references

- OAuth/deploy reference implementation: `cli/internal/auth/oauth.go`,
  `cli/internal/manifest/manifest.go`, `cli/internal/cmd/deploy.go`,
  `cli/internal/api/api.go`
- API contract: `services/api/openapi/openapi.yaml` (note the unpaginated
  lists and the Better Auth role-ownership note at line 155)
- Web deploy orchestration the native stream must digest-match:
  `apps/dashboard/lib/deploy.ts`, `apps/dashboard/lib/deploy-manifest.ts`
- Cookie-only member mutations driving the §5 bridge:
  `apps/dashboard/components/members/invite-member-form.tsx`
- Native SDK: https://native-sdk.dev · https://github.com/vercel-labs/native
  (v0.4.2; capabilities, packaging/signing, state/effects, frontend/WebView
  docs pages)
