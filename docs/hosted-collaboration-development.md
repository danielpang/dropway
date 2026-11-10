# Hosted collaboration: development setup and verification

This branch adds hosted editing of existing uploaded static site assets. A personal Anthropic or OpenRouter credential authorizes model calls. A dedicated Node 24 Fly worker claims Postgres jobs, stages an immutable site version in a fresh private Sprite, runs pinned Pi 1.5.0, captures the resulting files, destroys the Sprite, and submits an immutable candidate. Human approval and Apply remain separate actions. A clarification stores the transcript and modified file snapshot in Postgres and destroys the Sprite; an answer starts a fresh attempt with the same configuration and cumulative budget.

No production migration, deployment, paid model call, or Sprite provisioning was performed during implementation. Live development testing is the project owner's next step. Mock tests do not establish real network isolation or billing behavior.

## Configuration

Apply migrations through `0021_hosted_execution.sql` to the development database using the repository migration workflow. API and identity schemas must already be initialized. Keep the same development R2 storage and tenant setup used by the API.

| Process | Setting | Value / purpose |
| --- | --- | --- |
| API | `ROOM_EXECUTION_ENABLED` | `1` to enable; absent disables hosted endpoints |
| API | `ROOM_CREDENTIAL_MASTER_KEY` | Base64 encoding of exactly 32 random bytes; Fly secret, API only |
| API + worker | `ROOM_WORKER_TOKEN` | Same random secret, at least 32 characters |
| API + worker | `ROOM_API_PUBLIC_URL` | Exact development API HTTPS origin; loopback HTTP accepted locally |
| Worker | `ROOM_WORKER_ENABLED` | `1` to start |
| Worker | `SPRITES_API_TOKEN` | Dedicated development organization token, worker only |
| Worker | `SPRITES_ORGANIZATION` | Exact organization name checked against the Sprites API |
| Worker | `SPRITES_NAME_PREFIX` | Dedicated fleet prefix, for example `dropway-dev-` |
| Dashboard build | `NEXT_PUBLIC_COLLAB_EXECUTION` | `1` shows hosted controls after backend capability check |

The existing room UI and organization collaboration flags must also be enabled. Open `/rooms` directly in the dashboard; the default navigation does not advertise this gated feature. Keep credentials in Fly secrets or an ignored local environment file. Generate each random secret independently (for example, `openssl rand -base64 32`). Never give the worker the credential master key, database credentials, or personal model keys. Do not change the master key without migrating stored ciphertext; losing it makes saved keys unreadable.

The worker has no inbound public service. Build from the repository root with `services/room-worker/Dockerfile`. `fly.room-worker.toml` deliberately has no production app name: deploy explicitly to your own development worker app with `fly deploy --config fly.room-worker.toml --app <development-worker-app>`, then keep one worker machine running. Set secrets before starting it. No deployment workflow automatically enables this service.

For a local worker (Node 24+): `pnpm --filter @dropway/room-runtime build`, export the worker settings, then `pnpm --filter @dropway/room-runtime start`. The API URL must be reachable from that process. Public HTTPS is required from a Fly worker.

## Development acceptance walkthrough

1. Create a development room, attach a small existing HTML/CSS/JS site, and invite a second human reviewer. Confirm a nonmember cannot read its historical file bytes or job details.
2. In the room, save and validate your own provider key. Reopening the UI must show metadata only. Start with a small token/call/time/output budget and a provider-side spending limit you control. The application caps tokens, calls, active seconds and artifact bytes; it does not promise a dollar-denominated ceiling across model and Sprite charges.
3. Request a small visible edit. Verify a job is queued, claimed once, and produces a candidate whose baseline matches the pinned source. Confirm the live site is unchanged until another authorized human approves and explicitly applies it.
4. Ask for an ambiguous edit. Verify clarification appears, no Sprite remains waiting, and answering creates a new attempt with the prior modified files and transcript. Check counters aggregate across attempts and model/provider cannot silently change.
5. Cancel a running job; revoke or replace a credential mid-run; remove the initiator's room/site rights. Verify new model/tool calls stop and late output cannot produce a candidate. Requests already sent to providers may still incur charges.
6. Restart the worker mid-run. Verify lease expiry prevents stale output, retry gets a fresh capability, and expired Sprite leases are reaped. Reaper runs at startup and between serial jobs; if all workers are down, cleanup resumes when a worker restarts. Monitor the dedicated fleet separately while testing.
7. Run the opt-in live Sprite verification script described in `packages/room-runtime/scripts/SPRITES-VERIFICATION.md`. Verify DNS, direct IP, private IP and URL isolation in the actual development account, plus cancellation and cleanup. Do not enable production execution until these checks are satisfactory.
8. Open a historical thread after publishing a newer site version. Confirm it shows the immutable original bytes. Capture text/component/document/image points in the static rendition and verify the resulting anchor retains that exact version/hash/page.

The historical renderer intentionally reconstructs inert semantic HTML: it removes original scripts, CSS, forms, links and embeds. It displays signature-validated authorized raster images only. This enables trusted precise selections but is not a pixel-exact or interactive reproduction of the uploaded page. Opaque interactive previews remain page-only.

## Failure and recovery behavior

The API keeps encrypted keys separate from leased attempt and gateway capabilities. Every sensitive operation rechecks current rights, credential generation and deadline. Provider responses are bounded and buffered until usage is accounted; progress updates therefore arrive between accounted model turns rather than streaming raw tokens directly. Missing usage spends the conservative reservation and fails closed. Failed jobs surface sanitized codes, never provider response bodies or keys.

The worker has no local shell/file tool fallback. Sandbox networking is deny-all; this integration edits already-built assets and does not install dependencies. Source and output files are limited to 2 MiB each. Output is limited to 500 files and 10 MiB, additionally bounded by the job's configured output limit. Historical file reads are limited to 2 MiB per file. Existing review pins, outstanding candidates and active execution sources are retained by garbage collection.

For rollback, stop the worker and disable execution/dashboard flags before considering migration rollback. Migration Down drops hosted state; preserve needed development audit data first. Existing review and candidate application remain independent.
