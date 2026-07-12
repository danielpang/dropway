<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Multi-factor authentication — scoping

How Dropway adds MFA (TOTP authenticator app + one-time backup codes) for
every user, and lets business/enterprise org admins require it for all
members. This is the pre-implementation scoping doc: current state, design,
phased work plan, and the decisions already made.

## Decisions (settled)

- **MFA enrollment is available to every tier**, including free and OSS
  self-host. Security features are not paywalled; there is no tier gate on
  opting in.
- **MFA _enforcement_ is the plan lever**: an org-level "require MFA for all
  members" policy available on **business and enterprise only**. Free/pro
  orgs see the toggle as an upsell surface (same 402 + upgrade pattern as
  other quota-gated features).
- **Only owners and admins can toggle enforcement** — the existing
  `canManage()` boundary (owner ⊇ admin), enforced both in the dashboard and
  with a live role re-check in the Go API, matching every other org policy
  write.
- **Factors in v1: TOTP + backup codes.** No SMS (no SMS infra exists, and
  SMS is the weakest factor anyway). Passkeys/WebAuthn are a natural v2 on
  top of the same plumbing, not in scope here.

## Current state (what the code gives us)

- All interactive auth is owned by **Better Auth** inside the dashboard
  (`apps/dashboard/lib/auth.ts`); the Go API only verifies short-lived EdDSA
  JWTs and re-checks roles live. Better Auth ships an official `twoFactor`
  plugin (TOTP, backup codes, trusted devices), so the cryptographic core of
  MFA is on rails.
- Sign-in methods today: email+password, Google social login, and magic
  link. **All three must pass through the MFA challenge** or enforcement is
  decorative (see Risks).
- Org policy toggles have an established pattern to copy: a column on
  `app.org_meta`, a Go API write path with `IsAdminRole()` re-check
  (`services/api/internal/store/orgpolicy.go`), and a card in
  `app/(app)/settings/page.tsx` gated by `canManage()`.
- Tier truth lives in `app.org_meta.plan_tier`, enforced by the cloud-only
  quota provider (`cloud/quota`); OSS self-host has no billing and is treated
  as unlimited.
- There is **no per-user account/security page today** — `/settings` is
  org-scoped. Enrollment UI needs a net-new page.
- Better Auth's identity tables migrate via its own CLI, not goose; only the
  `org_meta` policy column is a hand-written goose migration.

## Design

### Enrollment (all tiers, all builds)

- Add `twoFactor()` to the plugin arrays in `lib/auth.ts` and
  `lib/auth-client.ts`; run the Better Auth migration (new
  `identity.twoFactor` table + `user.twoFactorEnabled` column).
- New **account security page** (`app/(app)/account/security/`): enroll via
  QR code + manual secret, verify one code to activate, show the 10 one-time
  backup codes exactly once with a download button (never emailed),
  regenerate codes, and disable (requires password + a valid code).
- New **`/two-factor` challenge step** in sign-in: TOTP entry, backup-code
  fallback, and a "trust this device for 30 days" option (Better Auth
  native). The challenge applies to password, Google, and magic-link
  sign-ins alike.
- Email notification (existing `lib/email.ts` + one new template) whenever
  MFA is enabled, disabled, or reset — a compromise tripwire.

### Enforcement (business/enterprise, admin-only)

- Goose migration `0013_mfa_enforcement.sql`: `app.org_meta.require_mfa`
  (full DDL and lock-step files in *Database migrations* below).
- Go API read/write endpoint mirroring `SetMcpEnabled`: live
  `IsAdminRole()` re-check, plus a tier check returning the standard 402 +
  upgrade-URL body for free/pro orgs. OSS self-host: always allowed.
- "Require MFA for all members" toggle card on the org settings page,
  visible to owners/admins; disabled-with-upgrade-prompt below business.
- **Enforcement seam**: on each authenticated dashboard request, if the
  active org has `require_mfa = true` and the user is not enrolled, every
  app route redirects to a mandatory setup page until enrollment completes.
  Enforcement is next-request (no live session revocation when the toggle
  flips) — v1 has no grace period; members are locked into setup
  immediately, which is the simpler and safer default.
- Members page shows per-member MFA status, since the first thing an admin
  asks after flipping the toggle is "who isn't enrolled yet?"

## Database migrations

Two migrations, in two different systems — plus two explicit "no migration
needed" calls.

### 1. Identity schema — Better Auth CLI (not goose)

The `identity` schema is owned and migrated by Better Auth (see
`db/migrations/app/README.md`), so the MFA credential storage is **generated,
not hand-written**: after adding `twoFactor()` to the plugin array in
`lib/auth.ts`, run `@better-auth/cli migrate` (the same jiti-loader flow used
for the existing identity tables). For reference, the shape it produces is
equivalent to:

```sql
-- Generated by Better Auth — do NOT hand-write this in db/migrations/app.
CREATE TABLE identity."twoFactor" (
    id          uuid PRIMARY KEY,           -- real UUIDs via advanced.database.generateId
    secret      text NOT NULL,              -- TOTP secret (encrypted at rest by the plugin)
    "backupCodes" text NOT NULL,            -- hashed one-time backup codes
    "userId"    uuid NOT NULL REFERENCES identity."user"(id) ON DELETE CASCADE
);

ALTER TABLE identity."user"
    ADD COLUMN "twoFactorEnabled" boolean DEFAULT false;
```

The authoritative DDL comes from the CLI against our pinned Better Auth
version — treat the above as the review checklist, not the source. Two
follow-ups belong to this migration:

- **Grant check**: the Go API's runtime role has read access to the identity
  schema from the baseline. If the members-page MFA status is served by the
  Go API (reading `identity."user"."twoFactorEnabled"` alongside its existing
  `identity.member` role reads), confirm the grant covers the new
  table/column; if status is served via Better Auth's server API in the
  dashboard instead, no grant is needed. The `twoFactor` secrets table should
  **never** be readable by the Go API either way.
- **Self-host docs**: OSS operators must run the Better Auth migrate step on
  upgrade; call it out in the release notes.

### 2. App schema — `db/migrations/app/0013_mfa_enforcement.sql` (goose)

One column on the org-policy anchor, following the `mcp_enabled`/`ai_enabled`
precedent exactly. No RLS or GRANT changes: `org_meta` already carries the
tenant policy and table grants, and a new column inherits them.

```sql
-- +goose Up
-- +goose StatementBegin
-- Org policy: when true, every member of the org must have MFA enrolled
-- before the dashboard serves them anything but the setup flow. Writable
-- only by owners/admins on business/enterprise (enforced in the API, not
-- the schema — same as every other org policy toggle).
ALTER TABLE app.org_meta ADD COLUMN require_mfa boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN IF EXISTS require_mfa;
-- +goose StatementEnd
```

Lock-step changes in the same PR (the standard trio for any `app` schema
change):

- `db/sqlc/schema.sql` — add `require_mfa boolean NOT NULL DEFAULT false` to
  the `app.org_meta` definition.
- `db/sqlc/queries` — extend the org-policy read to return `require_mfa` and
  add a `SetRequireMfa` update, mirroring the `SetMcpEnabled` pair; then
  regenerate sqlc types.

### 3. No billing migration

`billing.subscriptions.plan_tier` already carries
`free/pro/business/enterprise`; enforcement gating is a policy check in the
API layer (quota-provider style), not schema. Nothing changes under
`db/migrations/billing/`.

### 4. No audit migration

`app.audit_log.action` is free text; the new `mfa.enrolled`, `mfa.disabled`,
`mfa.reset_by_admin`, and `org.mfa_enforcement_changed` actions need no DDL.

A future grace period (a v1 non-goal) would be one more nullable
`org_meta` timestamp column — deliberately not added now.

### Recovery & operations

- **Admin MFA reset**: owner/admin can clear a member's second factor so a
  locked-out member re-enrolls at next sign-in. With enforcement on, this is
  the complete lockout story for orgs.
- Solo users (no org admin): backup codes are the recovery path, plus the
  support process. No email-OTP fallback — it would reduce MFA to the
  strength of the mailbox.
- Audit log entries (existing audit infrastructure): `mfa.enrolled`,
  `mfa.disabled`, `mfa.reset_by_admin`, `org.mfa_enforcement_changed`.

## Work plan

| Phase | Scope | Estimate |
| --- | --- | --- |
| 1 | Better Auth `twoFactor` plugin, identity migration, account security page, sign-in challenge step (all methods), enable/disable emails | ~1–1.5 weeks |
| 2 | `require_mfa` migration + Go API policy endpoint (role + tier checks), settings toggle card, enforcement middleware + mandatory setup redirect, member MFA status column | ~1 week |
| 3 | Admin MFA reset, audit log events, docs, e2e tests on challenge + enforcement flows | ~3–4 days |

Phases ship independently: phase 1 alone is opt-in MFA for everyone;
phase 2 adds the business/enterprise differentiator.

## Risks & watch items

- **Bypass via alternate sign-in methods** is the one way to get this wrong:
  the 2FA challenge must cover Google and magic link, not just passwords.
  Verified as a hard requirement in phase 1 with e2e coverage; the fallback
  lever if a method can't be covered is disabling that method for orgs with
  enforcement on.
- Better Auth version pin (1.6.19): confirm plugin behavior against this
  version early; a minor bump may be needed and touches the auth core.
- The MCP OAuth provider flow rides on the same Better Auth sessions —
  confirm the consent flow still works for an enforced-but-enrolled user and
  is blocked for an enforced-but-unenrolled one.

## Deliberate v1 non-goals

Passkeys/WebAuthn, SMS OTP, an enforcement grace period (immediate-only in
v1; a deadline + banner is a pure surface addition later), per-org allowed
factor configuration, and session-management UI beyond what enforcement
requires.
