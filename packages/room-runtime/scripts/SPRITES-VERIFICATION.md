# Development Sprite security gate

The real adapter uses the official `@fly/sprites` SDK, pinned to **0.2.4**, on Node **24 or later**. Lifecycle/policy calls use the SDK's HTTP transport; commands use its WebSocket framing. HTTP exec is deliberately unused because the official SDK warns that transport rechunking can corrupt that protocol.

Sources checked while implementing:

- [Official SDK](https://github.com/superfly/sprites-js), including the published 0.2.4 declarations and implementation.
- [Official networking contract](https://docs.fly.io/sprites/concepts/networking): empty rules are unrestricted; a deny-all policy blocks direct IPs except addresses resolved through an allowed domain, and blocks private IPs. These are provider claims until the live gate passes in your development organization.

## Local verification

```sh
pnpm --filter @dropway/room-runtime typecheck
pnpm --filter @dropway/room-runtime test
pnpm --filter @dropway/room-runtime build
```

The adapter tests use the actual SDK's HTTP serialization with mocked HTTP responses and mocked remote command streams. Guest-helper tests exercise fixture files in a disposable local directory, including binary export, traversal, symlinks, hard links, ambiguous edits, and digest tampering. They are tests of the helper, **not evidence of live Sprite isolation**. Production source has no local child-process fallback.

## Optional live gate — run yourself

No Sprite was provisioned by the implementation agent. Use a dedicated development organization and a token restricted to your chosen name prefix and minimal create/exec/policy/delete permissions. Confirm the token's actual organization before running; the adapter verifies it against API responses before creation.

This script creates at most two sequential disposable Sprites, each with a two-minute lease, a 512 MB memory policy with autoscaling disabled, and one requested CPU. The overall test deadline is five minutes. These are execution/resource controls, **not an account spending cap**. Actual charges depend on Fly pricing and elapsed resource/storage usage; inspect your account for the bill. An independent worker orphan reaper is still required if the script/process is killed before cleanup.

Set these through your secret manager or local shell without committing them:

- `SPRITES_TOKEN`: development control-plane token.
- `DROPWAY_SPRITES_ORG`: exact development organization slug returned by the API.
- `DROPWAY_SPRITES_PREFIX`: dedicated lowercase prefix, e.g. `dw-security-test-`.
- `DROPWAY_SPRITES_LIVE_APPROVED=yes`: your explicit approval to create the test resources.

Then run:

```sh
pnpm --filter @dropway/room-runtime build
node packages/room-runtime/scripts/sprites-live-verify.mjs --run-live
```

The gate verifies private URL settings, deny-all policy readback, denied DNS, raw-public-IP and private/metadata-IP connections, absent management credentials/host canary, staging/full artifact rehash, destruction during clarification, fresh resume, and in-flight cancellation/destruction. It prints pass/fail results without tokens or file bodies and attempts cleanup in `finally`. Inspect the dedicated prefix and manually remove anything remaining after a hard crash.

The script does not enable hosted production execution, publish a site, call a model provider, or apply a database migration. Until it passes, live egress, privilege-policy enforcement, cleanup and actual remote runtime compatibility remain unverified. Explicit Sprite pause/checkpoint functionality is not used: clarification always destroys the VM and persists only authorized files/transcript in Dropway.

## Adapter boundaries

All operations bind to one tenant/attempt and call the supplied backend authorizer afresh. Every tool/stage/export checks private URL, egress, privilege and memory policies. File tools use no-follow descriptor traversal rooted at the workspace. Shell/build runs in that workspace with a clean guest environment; **the guest VM is the shell security boundary**, not a chroot. Shell commands can access the guest's operating-system files; only authorized source is staged and no provider/Fly/database credentials are sent into it.

Artifact export accepts at most 500 regular files, 2 MiB each and 10 MiB total; symlinks and hard links are rejected. Every byte/hash/size and the Go-compatible manifest digest are rechecked in trusted code. Tool output is capped at 64 KiB; exceeding it cancels and destroys the attempt rather than silently truncating. Package downloads are disabled by default; the optional exact npm/node allowlist requires an explicit trusted build setting.
