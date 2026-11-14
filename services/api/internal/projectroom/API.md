# Project rooms API contract (phase 1)

The server-authoritative PostHog flag `project-collaboration` gates the review and hosted execution APIs. Only boolean `true` enables access for the verified user ID with `org_id` and `environment` evaluation properties; missing configuration and evaluation errors fail closed. Organization consent remains separate (`app.org_meta.project_rooms_enabled`, default `false`). An authenticated human can explicitly apply an approved candidate.

`GET /v1/project-collaboration/capabilities` requires current organization membership and returns `{enabled, execution_configured}` with `Cache-Control: no-store`. The first field is the authoritative rollout decision; the second reports valid vault/worker/API URL configuration independently of rollout. The dashboard uses these fields instead of build-time feature gates. Missing execution configuration leaves the API running and review available to eligible users.

An admin can still `PATCH /v1/orgs/project-rooms` with `{"enabled":false}` while rollout is unavailable, to withdraw consent and restore ordinary site deployment behavior. Enabling consent requires rollout eligibility. Active hosted attempts recheck their initiating user's rollout eligibility on claims, heartbeats, model calls, source reads, and candidate submission. Provider credentials remain encrypted and owner-scoped; hosted changes require human approval and application before publication.

## Rooms

Same-org rooms reference existing sites. They do not copy a site and do not change `sites.owner_user_id`.

Site reads stay org-wide. There is no finer per-user site ACL. Content edit rights are the existing rule: `allow_member_edits`, the site creator, or an org admin/owner.

| Room role | Read | Comment, reply, resolve, reanchor | Version sign-off | Run / submit candidate / approve-change / reject-change |
| --- | --- | --- | --- | --- |
| owner | yes | yes, if the caller can read the site | yes | yes, only if the caller can also edit site content |
| editor | yes | yes, if the caller can read the site | yes | yes, only if the caller can also edit site content |
| reviewer | yes | yes, if the caller can read the site | yes | no |
| viewer | yes | no | no | no |

The room owner manages members and may detach site references. Owners and editors may attach same-org sites they can read. Non-members in the same org get not-found. API keys cannot call room routes.

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
- `POST .../candidates/{id}/apply` verifies every base/proposed manifest and blob, then rechecks membership, site attachment, edit rights, exact identity, and the locked live baseline. It publishes and inserts an immutable application receipt in one transaction. The response includes `applied`, `changed`, `version_id`, `live_url`, and `candidate`. A retry at the same live version returns `changed: false`; a retry after later publication is a conflict and never rolls back the site.
- Applied candidates include `applied_version_id` in snake_case reads and `AppliedVersionID` in existing Go-shaped decision/application responses. Approval and application remain separate actions.

## Live mutation boundary

Credential class is stamped by the auth middleware after it verifies the bearer. It is not read from a request header or from an unverified token prefix.

- `human_session`: JWT whose verified audience is this API's interactive audience.
- `mcp_oauth`: JWT admitted only because its audience is an MCP resource audience.
- `org_api_key`: secret resolved by `app.resolve_api_key`. Agent keys and CI keys share this class.
- `dw_attempt_` and `dw_gateway_` tokens are not API credentials. They fail authentication.

For a site that is **not** in an enabled project room, prepare, finalize, and publish behave as before, including `sites:*` CI keys.

For a site that **is** referenced by a project room while `project_rooms_enabled` is true, `POST /v1/sites/{id}/deployments/prepare`, `POST /v1/sites/{id}/deployments`, and `POST /v1/sites/{id}/publish` (including rollback) require `human_session`. Org API keys and MCP OAuth receive 403. Omitting `X-Dropway-Attempt-Token` does not change that. Candidate apply rechecks the live baseline under the same site row lock publish uses.

Approval and publish lock `app.sites` before deciding, then lock the candidate. A second decision does not write a second audit. A hash or base version that belongs to a different candidate does not change either row.

`DELETE /v1/sites/{id}` and `PUT /v1/sites/{id}/access` use the same human-session rule on a controlled site. Org API keys and MCP OAuth cannot delete it, change its access mode, or turn project rooms off. A human room owner can detach a site with `DELETE /v1/project-rooms/{roomID}/sites/{siteID}`. That locks the site first and does not delete candidates. A version whose content hash equals a review candidate cannot use legacy publish, even after approval. It must pass complete artifact verification and the application receipt transaction through candidate Apply.

Publish and rollback re-check that membership inside the same transaction that locks `app.org_meta` and then `app.sites`. Attaching a site, approving a candidate, and applying a candidate take those locks in that order. A non-human publish that is still waiting on the site lock sees a membership committed by the transaction that held the lock. Disabling `project_rooms_enabled` is a human admin action; it locks the same org row, so a key cannot observe the old flag and then publish after the flag has changed.

Site-linked chats and memories are protected as described below. Org-wide skill writes and chats/manual memories with no site remain available to org keys; a non-null key `site_id` cannot access those org-wide routes.

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
- Every project-room route rejects API keys. Comment and room reads are an MCP OAuth grant, not an API-key scope.

## Room read contract

Reads accept `human_session` or `mcp_oauth` and recheck org membership and room membership on every page. Writes accept only `human_session`.

| Method | Path | Response notes |
| --- | --- | --- |
| GET | `/v1/sites/{id}/comment-pages?cursor&limit` | `{items, next_cursor, comment_content: untrusted_data}`. Items are `feed` or `room`, including resolved and orphaned anchors while the site is attached. |
| GET | `/v1/project-rooms/threads/{threadID}?cursor&limit` | Thread pin, every anchor revision, and one reply page. |
| GET | `/v1/project-rooms/{roomID}` | `{room, members, sites}` in snake_case. |
| GET | `/v1/project-rooms/{roomID}/threads?cursor&limit` | Thread heads. Replies are on the thread route. |
| GET | `/v1/project-rooms/{roomID}/candidates?cursor&limit` | Candidate identity and summary. No diff body. |
| GET | `/v1/project-rooms/{roomID}/candidates/{candidateID}` | One candidate. Summary is untrusted. |
| POST | `/v1/project-rooms/{roomID}/members` | Owner. `{user_id, role}` of `editor`, `reviewer`, or `viewer`. |
| PATCH | `/v1/project-rooms/{roomID}/members/{userID}` | Owner. Cannot change the owner. |
| DELETE | `/v1/project-rooms/{roomID}/members/{userID}` | Owner. Cannot remove the owner. The next read by that user is not-found. |
| POST | `/v1/project-rooms/{roomID}/sites` | Owner or editor with site read rights. `{site_id}`. |
| DELETE | `/v1/project-rooms/{roomID}/sites/{siteID}` | Owner. Detach only. |
| POST | `/v1/project-rooms/{roomID}/threads/{threadID}/resolve` | Owner, editor, or reviewer who can read the site. |
| POST | `/v1/project-rooms/{roomID}/threads/{threadID}/reanchor` | Same callers. `{kind, anchor, anchor_state, reason}` appends a revision and does not change the pin. |

`GET /v1/project-rooms` and `POST /v1/project-rooms` still use the existing Go JSON names. Page order is `(created_at, id)` as text, merged site comments also order `feed` before `room` at the same timestamp. Default limit 50, maximum 100.

MCP tools `list_site_comments`, `get_comment_thread`, `get_project_room`, `list_room_threads`, `list_room_candidates`, `get_room_candidate`, and `get_candidate_diff` forward the verified OAuth token to those GET routes only.

## Candidate diff

`GET /v1/project-rooms/{roomID}/candidates/{candidateID}/diff` is a human or MCP read. It loads the pinned base version and the site version whose content hash is the candidate hash. It recomputes each manifest digest and each included blob hash. Any mismatch is 409 and the response contains no file text.

```json
{
  "candidate_id": "uuid",
  "site_id": "uuid",
  "base_version_id": "uuid",
  "base_content_hash": "hex",
  "content_hash": "hex",
  "verified": true,
  "truncated": false,
  "omitted_files": 0,
  "comment_content": "untrusted_data",
  "files": [
    {
      "path": "index.html",
      "change": "modified",
      "binary": false,
      "before_sha256": "hex",
      "after_sha256": "hex",
      "before_size": 1,
      "after_size": 2,
      "before": "old",
      "after": "new",
      "before_truncated": false,
      "after_truncated": false,
      "untrusted": true
    }
  ]
}
```

`change` is `added`, `removed`, `modified`, or `unchanged`. Unchanged files omit bodies. Binary files and files over 64 KiB are metadata only. Text bodies stop at 2048 bytes and then end with `\n...[truncated]`. More than 80 paths sets `truncated` and `omitted_files`.

`GET /v1/sites` and `GET /v1/sites/{id}` include `current_content_hash` for the live version. It is omitted when nothing is published. Compare it with `base_content_hash`.

## Site-linked chats and memories

A non-human caller cannot create, append, delete, move, or retarget a chat attached to a site in an enabled room, and cannot edit or delete a memory whose source is that site's version or attached chat. The store locks the org row, then the site row, in the same transaction. Chats and manual memories with no site stay available to org keys. Skills have no site foreign key, so org-wide skill writes are unchanged.

## Activity and read state

`GET /v1/project-rooms/{roomID}/activity?cursor&limit` accepts human sessions and MCP OAuth. It returns `{items, next_cursor, unread}` newest first. Each item contains `id`, `room_id`, `actor_user_id`, `kind`, `created_at`, and optional `thread_id`, `site_id`, and `version_id`. It contains references, never comment bodies. Cursors are bound to the room and membership is rechecked for each page. Activity for detached or deleted sites is excluded from both results and unread counts.

`POST /v1/project-rooms/{roomID}/read` accepts human members only and returns `{last_read_at}`. It marks only that human's room activity as read. A GET, MCP read, or mark-read never approves, resolves, or applies a candidate.

## Pagination and publication recovery

Review cursors are authenticated and encrypted, bound to org, connecting user, endpoint namespace and target, with a fixed creation watermark and 30-minute expiry. Key rotation invalidates outstanding cursors. All API replicas must use the same `EDGE_SIGNING_KEY`; the API derives a separate cursor key from it. Development stores use a random ephemeral key. Unknown filters are rejected rather than silently ignored.

Thread replies and anchor revisions paginate independently: `cursor` / `next_cursor` for replies, `revisions_cursor` / `next_revision_cursor` for anchor history. Original thread pins stay immutable. Responses are byte-bounded with explicit continuations; a single oversized item returns 413 rather than truncating silently. Review reads share a per-process user/org budget of 120 requests/minute with burst 30; throttled reads return 429 and `Retry-After`.

Candidate application writes a durable projection intent with its receipt. The API tries immediate delivery and polls recovery every 30 seconds with a 20-second deadline. Recovery derives the current database route under publication locks, including current access policy and custom hosts. A successful site is reconciled again after five minutes to repair delayed stale writes from legacy handlers. Delivery is bounded to 100 sites per org per pass, with randomized tenant scheduling. KV convergence is eventual; this does not promise linearizable edge reads.

Apply migration 0020 before starting this API revision. The single server-evaluated PostHog `project-collaboration` flag and organization opt-in remain off by default; migration, flag enablement, and production deployment are separate rollout actions. See [current rollout configuration](../../../../docs/hosted-collaboration-development.md#feature-rollout).
