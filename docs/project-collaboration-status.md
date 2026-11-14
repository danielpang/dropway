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

Both the dashboard review flag and organization setting remain off by default. No production migration, deployment, or provider-key collection is part of this continuation.

## Remaining release work

The complete PRD is **not release-ready** merely because the human review changes pass tests.

1. **Hosted execution:** encrypted personal provider credentials with validation/replacement/revocation; curated pinned model/preset selection; bounded gateway accounting; durable task/attempt/session records; actual API-to-worker orchestration; a real Sprite control-plane adapter; authorized source hydration and candidate capture; and thread-based clarification/resume with no waiting VM. The existing Pi spike is not this integration.
2. **Live isolation verification:** real Sprite DNS/egress/direct-IP/private-IP enforcement, private URLs, cancellation/destruction/orphan cleanup, sleep/wake/checkpoints, and billing ceilings. Mock tests cannot establish these properties.
3. **Precise HTML selections:** text/component/document-point targeting needs a trusted rendering architecture that satisfies the sandbox constraints. Opaque frames remain page-only; image pins are measured by the parent. Do not use wildcard handshakes or add same-origin privileges to claim support.
4. **Original artifact reopening:** thread permalinks preserve original version/page/anchor metadata. A durable authorized preview of an old version must be verified separately; a live site URL is not an immutable preview.
5. **Release verification:** signed-in browser coverage of the complete review cycle, unsupported-browser behavior, a production migration/rollback rehearsal, and the remaining acceptance criteria in the technical plan. Enable flags only as an explicit rollout action.

## Validation

Local validation on the continuation branch:

- `go test ./...`, `go build ./...`, `go vet ./...`, and `go build -tags cloud ./...` passed.
- Dashboard: 303 tests in 37 files passed, including all three real Chromium sandbox probes. Dashboard lint passed.
- `pnpm -r typecheck` passed for every workspace package; SDK schema generation/build passed.
- Pi runtime spike: 18 mock tests passed; typecheck passed. This does not establish live Sprite behavior.
- Real Postgres tests cover candidate application races, activity authorization, approval races, publish protection, version-insert protection, tenant RLS, complete comment/history pagination, GC retention, projection outage/restart recovery, stale-edge repair, and migration 0020 Down/Up.

An authenticated full-browser review walkthrough, Firefox/Safari verification, and hosted execution end-to-end tests were not performed. No production migration, paid resource provisioning, deployment or automatic merge occurred.
