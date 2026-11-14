# Project rooms API contract (phase 1)

This slice is default-off (`app.org_meta.project_rooms_enabled`, default `false`).
It does not run Pi, start Sprites, store provider keys, or publish a candidate.

## Rooms

Same-org rooms reference existing sites. They do not copy a site and do not change `sites.owner_user_id`.

Site reads stay org-wide. There is no finer per-user site ACL. Content edit rights are the existing rule: `allow_member_edits`, the site creator, or an org admin/owner.

| Room role | Read | Comment, reply, resolve, reanchor | Version sign-off | Run / submit candidate / approve-change / reject-change |
| --- | --- | --- | --- | --- |
| owner | yes | yes, if the caller can read the site | yes | yes, only if the caller can also edit site content |
| editor | yes | yes, if the caller can read the site | yes | yes, only if the caller can also edit site content |
| reviewer | yes | yes, if the caller can read the site | yes | no |
| viewer | yes | no | no | no |

The room owner manages members and site references. Non-members in the same org get not-found. API keys cannot call room routes.

Version sign-off (`POST /v1/project-rooms/{roomID}/version-signoffs`) records that a person has looked at a specific version. It does not publish and it does not authorize applying a change.

## Anchors

Thread pins are immutable: `site_id`, `version_id`, `content_hash` (copied from the version), and `page`. Kinds are `point`, `text`, `component`, and `page`. A point is normalized coordinates plus viewport, rendition, and scroll, with optional image intrinsic coordinates. It is not a source mapping. A page anchor has no position, so unsupported HTML can still be commented on. Reanchor appends a revision and keeps the old one. Orphaning is explicit. Feed comments stay in `app.post_comments`.

## Approve-change

`review_candidates` is a separate state machine from version sign-off.

- States: `pending`, `approved`, `rejected`, `conflict`. There is no timeout and unknown states are denied.
- A human session must present the exact candidate id, content hash, base version id, and base content hash.
- API keys, attempt tokens, and gateway tokens cannot approve, reject, or apply.
- Repeating approve on the same artifact does not write a second effect and cannot be replayed onto another candidate or version.
- If the live baseline is not that base version, the candidate becomes `conflict` and is not applied.
- Reject is terminal.
- `POST .../candidates/{id}/apply` checks those rules and then returns `candidate application is not enabled`. It does not call publish.

## Legacy publish

`POST /v1/sites/{id}/publish` and `POST /v1/sites/{id}/deployments` reject `X-Dropway-Attempt-Token`, `X-Dropway-Gateway-Token`, and bearer tokens prefixed `dw_attempt_` or `dw_gateway_`.

An org API key with `sites:*` and a null `site_id` can still publish. That is the remaining agent-credential bypass until agent credentials are a distinct class from CI keys. Tests still needed:

- an agent-class credential receives 403 from publish, rollback (publish of an older version), and deployment finalize
- approve and reject racing on one pending row leave exactly one terminal state and one audit effect
- a candidate whose base version is no longer live stays `conflict` under a concurrent publish
- an approval for candidate A cannot be replayed against candidate B or another base version

## Execution seam

Pi is an orchestration harness and runs only in a future Dropway worker. Sprites are remote file, shell, and build tools. The worker calls the model gateway with the initiating user's credential reference. Sprites receive no provider key and no Fly management token. Fly credentials stay in the control plane.

`POST /v1/project-rooms/{roomID}/executions` validates the caller and returns `execution_disabled` with blockers:

- `credential_store_unavailable` — no encrypted secret store exists; do not send key material
- `pi_sdk_unverified` — custom tools, default-tool disabling, extension autoload, session persistence, cancel, and usage are not verified
- `sprites_execution_disabled` — do not assume isolation, egress policy, destroy, or budgets
- `session_persistence_deferred`

Clarification (`POST .../sessions/clarify`) plans a Dropway-persisted session, releases the worker and Sprite, and continues as a fresh attempt with the same pinned provider, model, harness id, and harness version. It does not persist yet.

Billing v1 uses the initiating user's own credential. A teammate or room-owner key is rejected.

Provider keys, if added later, are write-only ciphertext, masked in metadata, revocable, and scoped to that user. They must not appear in SQL rows as plaintext, logs, events, threads, or responses. The gateway must reject unknown models and user-supplied base URLs. The current allowlist is `anthropic` / `openrouter` with `claude-sonnet-4-5`, `claude-opus-4-1`, and `claude-haiku-4-5` (OpenRouter ids are prefixed `anthropic/`).

## API key grant

`app.resolve_api_key` now returns `scopes` and `site_id`.

- `sites:*` and a null `site_id` keep the previous key behavior outside project rooms.
- A non-null `site_id` may only call `/v1/sites/{that id}/...` plus `GET /v1/me`.
- `sites:read` is read-only on site routes. `sites:write` is the mutation scope. Empty or other scopes do not grant the legacy full key.
- Every project-room route rejects API keys.
