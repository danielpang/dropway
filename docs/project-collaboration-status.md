# Project collaboration implementation status

Audited 2026-11-10 against the four-page project collaboration PRD, the technical implementation plan, and GitHub PRs 159 onward. The PRD explicitly prioritizes human review before the first hosted source integration.

The technical plan was recovered from the saved companion `Dropway_ technical implementation plan_files/frame.html`. It authorizes phased implementation behind a default-off flag and explicitly excludes production migrations, deployment, paid provisioning, and automatic merges. It requires durable publication effects and authenticated, expiring pagination in addition to the PRD review workflow.

## Existing implementation

All collaboration PRs below were open and unmerged at audit time. “Implemented” means code exists on the PR branches, not that the feature is deployed.

| PR | Implemented | Limit at that point |
| --- | --- | --- |
| [159](https://github.com/danielpang/dropway/pull/159) | Anchor types, immutable version pins, image coordinates, text quotes, sandbox checks | No accepted precise-selection bridge in opaque HTML; page/image modes only |
| [160](https://github.com/danielpang/dropway/pull/160) | Room/member/site schema, tenant isolation, threads, replies, sign-off, candidate decisions, attempt token seams | Apply and execution refused; no room UI |
| [161](https://github.com/danielpang/dropway/pull/161) | Verified human/MCP/key credential classes, live mutation protection, locking and race tests | Apply refused; site-linked source protection incomplete |
| [162](https://github.com/danielpang/dropway/pull/162) | Default-off room UI, comments, sign-off and candidate decisions | Read APIs and management controls incomplete |
| [163](https://github.com/danielpang/dropway/pull/163) | Pinned Pi 1.5.0 compatibility spike; remote tool interfaces, gateway capability, cancellation, clarification port | Mock control plane only; no database clarification adapter, provider secret store, gateway service, or API execution wiring |
| [164](https://github.com/danielpang/dropway/pull/164) | Paginated authorized comment/candidate reads, MCP tools, member/site management, resolve/reanchor | No verified diff endpoint |
| [165](https://github.com/danielpang/dropway/pull/165) | Room UI connected to read/management APIs | Diff unavailable; Apply disabled |
| [166](https://github.com/danielpang/dropway/pull/166) | Verified candidate diffs, live content hash, protected version insertion and site-linked chat/memory writes | Apply still disabled |
| [167](https://github.com/danielpang/dropway/pull/167) | Verified diff viewer, stale/unverifiable approval blocking | Apply still disabled; hosted execution unavailable |
| [168](https://github.com/danielpang/dropway/pull/168) | Password-site access correction | Unrelated to collaboration; already merged into main |

The continuation branch integrates PR 167 (which includes the review stack), PR 163, and current main without rewriting existing branch history.

## Continuation work

- Human candidate application: verify the complete artifact, recheck room/site edit rights and exact approval under locks, publish and record a receipt in one transaction. Retry cannot apply twice or overwrite a later publication.
- Migration `0020_candidate_applications.sql` records application independently of terminal human decisions. Existing migration 0019 is unchanged.
- Dashboard Apply uses the candidate's immutable identity, requires approval and a verified diff, blocks concurrent clicks, and refreshes the live baseline.
- Room owners can update/remove members. Editors can attach authorized same-org sites without changing ownership or access.
- Room activity is paginated and membership checked. Detached/deleted sites are excluded. Explicit mark-read updates only the current human's read state; MCP reads do not acknowledge it.
- Thread links retrieve the linked thread even when it is outside the first page. The original immutable pin remains visible; collapsed anchor history has independent pagination and preserves reanchor reasons. Replies also have independent pagination.
- Room/site switches discard stale asynchronous reads so a prior room's data does not remain actionable.
- OpenAPI and the generated SDK describe the new activity, read acknowledgement, and application endpoints.
- Authenticated opaque pagination binds tenant, caller, endpoint target, expiry, and traversal watermark. API replicas derive the cursor key from a purpose-separated edge signing key; rotation requires a fresh traversal.
- Review reads share a per-process authenticated user/org token bucket (120/minute, burst 30).
- Durable projection effects recover post-commit failures from current database routes, never an obsolete route snapshot. A 30-second worker retries queued effects; successful sites reconcile again after five minutes to repair delayed legacy writes. Edge consistency remains eventual.
- Garbage collection retains original thread versions and outstanding candidate bases/proposals.

## Hosted continuation (items 1–3)

The next branch, `feat/hosted-collaboration-loop`, adds:

- AES-GCM encrypted personal provider credentials with owner-bound validation, replacement/generation invalidation and revocation; no secrets returned to the dashboard or model worker.
- Postgres jobs, leased attempts, bounded progress events and persisted clarification sessions; queue/claim/terminal submission idempotency, cumulative token/call/time budgets and current membership/site/credential rechecks.
- A capability-bound Anthropic/OpenRouter gateway with conservative reservations and verified usage accounting, fixed provider origins and no fallback. Model output is buffered until usage settles.
- A dedicated Node 24 Fly worker and pinned Sprites SDK adapter. It stages authorized immutable uploaded assets, exposes remote-only tools, exports verified files, destroys the Sprite and then submits a pending candidate. Clarification preserves modified files and transcript without a waiting VM.
- Credential/model/budget setup and explicit run, progress, cancel and clarification controls in the dashboard. A single server-evaluated PostHog capability controls API and dashboard access; workers remain running for cleanup.
- Authorized immutable historical file reads and a strict static HTML rendition with precise text/component/document/image point selections. Source scripts, CSS, URLs, forms and embeds are removed; this is a semantic review rendition, not pixel-exact interactive playback.
- Local security coverage for capability scope, missing usage, stale baselines, revoked credentials, lease expiry, cleanup, hostile HTML and forbidden sandbox operations. An opt-in live Sprite verification script is provided for the project owner's development testing.

See [development setup and acceptance walkthrough](hosted-collaboration-development.md) for configuration, limits and recovery behavior.

## Remaining release work

1. **Live development verification:** the project owner will test actual Sprite and model integration. DNS/direct-IP/private-IP isolation, private URLs, cancellation/destruction, orphan recovery and provider/Sprite charges still require live observations. No paid resources were provisioned during implementation.
2. **Release verification:** signed-in browser coverage of the complete hosted review cycle, Firefox/Safari behavior, a deployment/migration/rollback rehearsal and remaining technical-plan acceptance criteria.
3. **Later source integrations:** GitHub checkout/build is intentionally outside this first integration; the selected scope is existing uploaded HTML/CSS/JS assets. Network access for package installation remains disabled.

No production migration, deployment or automatic merge occurred. The implemented controls and local tests do not replace the live release checks above.

## Validation

Earlier human-review continuation validation (PR 169):

- `go test ./...`, `go build ./...`, `go vet ./...`, and `go build -tags cloud ./...` passed.
- Dashboard: 303 tests in 37 files passed, including all three real Chromium sandbox probes. Dashboard lint passed.
- `pnpm -r typecheck` passed for every workspace package; SDK schema generation/build passed.
- Pi runtime spike: 18 mock tests passed; typecheck passed. This does not establish live Sprite behavior.
- Real Postgres tests cover candidate application races, activity authorization, approval races, publish protection, version-insert protection, tenant RLS, complete comment/history pagination, GC retention, projection outage/restart recovery, stale-edge repair, and migration 0020 Down/Up.

Hosted continuation validation:

- Full Go unit suite and cloud build passed; `go vet ./...` passed.
- Dashboard: 312 tests passed, including Chromium hostile-content and selection probes; lint passed.
- All workspace typechecks passed; OpenAPI SDK generation passed.
- Real local Postgres plus fake-provider integration covers credential lifecycle, claims, source binding, budget accounting, immutable candidate capture, retries, clarification/resume, cancellation, expired leases and revocation.
- Runtime: 36 tests pass, including actual Pi orchestration against fake API/Sprite boundaries, scoped model transport and guest filesystem security.
- The dedicated Node 24 worker image builds and loads its entrypoint with network access disabled and no credentials.

An authenticated full-browser hosted walkthrough and Firefox/Safari verification were not performed. Real model calls and Sprite isolation checks remain for the owner's development tests. No production migration, paid resource provisioning, deployment or automatic merge occurred.

## Merge-risk hardening

- Clean dashboard container builds now copy and compile the SDK before Next.js typechecking.
- Legacy publish/rollback is restored after a site detaches or rooms are disabled; currently protected candidate artifacts still require the human Apply path.
- Read-only schema preflight runs during Fly release and normal API startup. Migration 0022 adds indexed due-projection discovery; migrations remain separate from deployment.
- Shared organization locks allow unrelated sites to publish concurrently. Site enrollment and flag changes retain their synchronization; moving a chat between sites locks both in stable order.
- Room enable/attach controls explain the effect on automated API-key/MCP writes. Existing unrestricted-key behavior and old-binary resolver compatibility have regression coverage.

See [API rollout instructions](../deploy/API-ROLLOUT.md). This work reduces known rollout risks; live Sprite/provider acceptance and production capacity verification remain separate release checks.

Rollout-fix validation: clean dashboard Docker build; all 8 full-stack browser tests with collaboration disabled (sign-in/signup, upload/live deploy, OAuth, API-key SDK deployment); full Go unit suite; real Postgres schema preflight/old-query/API-key workflow tests; candidate rollback/concurrency and approval races. The schema test verifies missing migrations and revoked resolver execution grants fail preflight, then migrations restore readiness. A transient Docker startup reset in one approval test passed on isolated retry.

## Unified feature rollout

[PostHog `project-collaboration`](https://us.posthog.com/project/479412/feature-flags/1059373) now replaces the four former environment toggles. The flag is inactive at 0% as of 2026-11-10. The API reuses `POSTHOG_KEY` / `POSTHOG_HOST`, evaluates the authenticated user with `org_id` and `environment`, and supplies dashboard capabilities. Organization opt-in and authorization remain independent requirements. Workers continue cleanup while the API gates claims. See the [development configuration](hosted-collaboration-development.md#feature-rollout) for the identity contract and disable behavior.

Dashboard capability validation: 315 tests in 41 files, including four Chromium security probes, plus typecheck and lint passed. No paid model calls or Sprite provisioning were made.
