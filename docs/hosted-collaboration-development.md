# Hosted collaboration: development setup and verification

This branch adds hosted editing of existing uploaded static site assets. A reusable personal or team Anthropic, OpenRouter, or OpenAI connection authorizes model calls. A dedicated Node 24 Fly worker claims Postgres jobs, stages an immutable site version in a fresh private Sprite, runs pinned Pi 1.5.0, captures the resulting files, destroys the Sprite, and submits an immutable candidate. Human approval and Apply remain separate actions. A clarification stores the transcript and modified file snapshot in Postgres and destroys the Sprite; an answer starts a fresh attempt with the same configuration and cumulative budget.

No production migration, deployment, paid model call, or Sprite provisioning was performed during implementation. Live development testing is the project owner's next step. Mock tests do not establish real network isolation or billing behavior.

## Rollout safety

Follow [API schema rollout](../deploy/API-ROLLOUT.md) before deploying. Fly runs a read-only `--check-schema` release check; a missing migration or runtime grant rejects the new release before serving instances are updated. The API repeats this check before opening its listener. Apply migrations separately; keep the expanded schema if rolling back application code.

The ordinary API-key publishing/rollback path remains available for sites outside enabled rooms, including sites detached from rooms or in organizations that disable rooms. Enrolled sites continue to require human review. The enable/attach UI explains this change before the action. Disabling a room does not revoke an API key's own explicit scope/site restrictions.

Normal keys created by Dropway have `sites:*` and no site restriction, so their existing unenrolled-site workflows remain compatible. Narrower scopes or a site restriction are security boundaries and remain enforced; do not broaden a key's privileges just to bypass an expected denial. Before rollout, audit any custom-provisioned restricted keys and test their intended routes.

Ordinary site mutations share the organization configuration lock and lock only their target site exclusively. This preserves concurrent publishing to different sites while still serializing enrollment and configuration changes. Recovery polls only organizations with due candidate projections, so organizations with no collaboration history do not incur one query each every polling interval. These concurrency properties have local Postgres regression tests; production capacity still requires normal monitoring.

## Feature rollout

[PostHog `project-collaboration`](https://us.posthog.com/project/479412/feature-flags/1059373) is the single feature flag for review and hosted execution. It is inactive with a 0% rollout as of 2026-11-10. The API evaluates it using the existing `POSTHOG_KEY` and `POSTHOG_HOST`; no collaboration-specific environment toggle or dashboard rebuild is required. Evaluation uses the authenticated user ID as `distinct_id`, `org_id` and `environment` as person properties. Missing configuration or evaluation failure denies access.

The dashboard reads `GET /v1/project-collaboration/capabilities` through its authenticated API client. The response contains `enabled` and `execution_configured`: the first is the PostHog decision; the second reports runtime configuration readiness, not another feature flag. Pages fail closed and server actions recheck capability. The API enforces authorization independently of the UI.

Organization availability (`app.org_meta.project_rooms_enabled`) defaults on after migration 0026. Existing explicit opt-outs remain enforced, along with room/site permissions. Turning the PostHog flag off does not detach sites or bypass their existing human-review protections. The worker stays running for cleanup; the API gates new claims and sensitive operations using the initiating user's current decision. Keep the worker running while draining/reaping existing Sprite leases, even when rollout is disabled.

## Configuration

Apply migrations through `0025_project_guest_shares.sql` to the development database using the repository migration workflow. API and identity schemas must already be initialized. Keep the same development R2 storage and tenant setup used by the API.

| Process | Setting | Value / purpose |
| --- | --- | --- |
| API | `ROOM_CREDENTIAL_MASTER_KEY` | Base64 encoding of exactly 32 random bytes; Fly secret, API only |
| API + worker | `ROOM_WORKER_TOKEN` | Same random secret, at least 32 characters |
| API + worker | `ROOM_API_PUBLIC_URL` | Exact development API HTTPS origin; loopback HTTP accepted locally |
| Worker | `SPRITES_API_TOKEN` | Dedicated development organization token, worker only |
| Worker | `SPRITES_ORGANIZATION` | Exact organization name checked against the Sprites API |
| Worker | `SPRITES_NAME_PREFIX` | Dedicated fleet prefix, for example `dropway-dev-` |

Enable the PostHog flag for the intended development user/organization and opt that organization into rooms. Open `/rooms` directly in the dashboard; the default navigation does not advertise this gated feature. Keep credentials in Fly secrets or an ignored local environment file. Generate each random secret independently (for example, `openssl rand -base64 32`). Never give the worker the credential master key, database credentials, or personal model keys. Do not change the master key without migrating stored ciphertext; losing it makes saved keys unreadable.

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

To pause rollout, disable `project-collaboration` in PostHog and keep workers running to clean up existing leases. For an application rollback, retain the expanded schema and redeploy a compatible image as described in the API rollout guide. Do not use migration Down to disable a feature: it drops hosted state and review records.

## Shared connections and mention requests

Connections are reusable across all rooms and sites in the same organization. A personal connection is visible and usable only by its creator. An organization owner/admin may create a team connection, which authorized room editors can use without seeing the key. Owners/admins manage team connections; room roles and site edit rights still control who can request changes. Scope and provider are immutable: create a new connection to change them. Rotating or revoking a connection invalidates previously authorized attempts through its generation/current status.

In `/rooms`, select a room and open **Hosted revisions → Your provider connections** to create, validate, replace, or revoke a connection. Choose **Personal** or **Team** when creating it (Team requires organization admin/owner). Existing connections retain personal scope after migration. OpenAI connections support `gpt-4.1` and `gpt-4.1-mini` through the bounded gateway; raw provider keys never enter the Sprite. Model usage is billed to the selected provider account.

Ordinary comments do not start AI. A standalone `@dropway` mention in a new comment or reply requests a revision only with explicit connection/model/budget settings and paid-use authorization in the composer. An incomplete setup fails visibly; it never silently selects another person's personal key. The server atomically commits the reply and queued job and deduplicates retries. Historical threads cannot launch edits against a newer live baseline. The separate **Run hosted revision** control remains an explicit alternative trigger.

AI creates a candidate only. Review its summary and file changes, then **Accept change** or **Reject change**. Acceptance records approval; the separate **Apply** action publishes. Rejecting leaves the live site unchanged. No comment, model response, or mention automatically approves or publishes a candidate.

Migrations 0023–0024 add credential scope/OpenAI support and reply/job linkage. Keep the expanded schema on application rollback; older workers must be drained/replaced before enabling new provider jobs. Production acceptance should exercise team revocation, mention retries, all three providers, and rejection with disposable sites under the PostHog test cohort. Local tests use fake provider responses, not paid calls.

## External project guests

A project can be shared by email without adding the recipient to its organization. Invite a **Viewer** for read-only access or a **Reviewer** for discussions and version sign-off. Guests cannot run AI, use team credentials, accept/apply proposed changes, or publish. Shared projects appear in a separate **Shared with me** area; guests do not need to create an organization.

Invitation acceptance requires a human session with the invited email currently verified. Existing users can use their normal sign-in method; new users can use email magic-link sign-in. An invitation link alone does not grant access. Pending invitations expire; accepted access continues until revoked. Resending replaces the pending invitation link. Owners can list invitations and revoke access.

A share covers the project's attached sites and discussions, including sites attached later. Detaching a site removes it from guest access. Guest previews are sanitized static renditions: uploaded JavaScript does not run, and sharing never makes the underlying site public or grants general access to its gated live URL. Revocation prevents subsequent authorized requests; it cannot erase content already delivered to a recipient.

Both `allow_external_sharing` and `project_rooms_enabled` must be enabled in the host organization. The existing PostHog flag also evaluates the guest user in the host organization's context, so include invited test users in a restricted rollout cohort. All guest operations check the current share, verified identity, policy, and rollout decision. The normal organization API continues to require organization membership.

Apply migration `0025_project_guest_shares.sql` before deploying this API. The API schema preflight checks the new table and discovery-function grants. Invitations reuse `MAIL_SMTP_URL` and `MAIL_FROM`; there is no new email service or feature toggle. The sharing UI reports a mail-server rejection or missing SMTP configuration instead of claiming delivery. SMTP acceptance is not proof of inbox delivery. Retain the additive schema on application rollback.
