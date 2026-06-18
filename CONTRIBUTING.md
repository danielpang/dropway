<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Contributing to Dropway

Thanks for your interest in Dropway! This guide covers how to contribute to the
**FSL-licensed core**.

> The proprietary `cloud/` and `ee/` trees are not open for outside contribution.

## Developer Certificate of Origin (DCO): required

Every commit must be **signed off** under the
[Developer Certificate of Origin](https://developercertificate.org/) (DCO). This is a
lightweight, legally meaningful statement that **you wrote the patch or otherwise have the
right to submit it** under the project's license. We use the DCO (not a CLA) for the FSL core.

By signing off you certify the DCO 1.1, reproduced here:

```
Developer Certificate of Origin
Version 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

### How to sign off

Add a `Signed-off-by` trailer to every commit message that matches your real name and the
email you commit with. The easiest way is the `-s` / `--signoff` flag:

```sh
git commit -s -m "fix: correct RLS policy for site_versions"
```

This appends, e.g.:

```
Signed-off-by: Jane Doe <jane@example.com>
```

If you forgot, amend the last commit:

```sh
git commit --amend -s --no-edit
```

Or sign off a whole branch with a rebase:

```sh
git rebase --signoff main
```

A DCO check runs in CI; PRs with unsigned commits will be asked to add the sign-off.

## Workflow

1. **Open an issue first** for anything non-trivial so we can agree on the approach.
2. **Branch off `main`**: never commit directly to it.
3. **Keep changes idiomatic and well-commented**, matching the surrounding style.
4. **Make CI green locally** before pushing:
   - Go: `go build ./... && go vet ./... && go test ./...` and `go build -tags cloud ./...`
   - SQL: apply `db/migrations/app/` with goose against a local Postgres (see
     [`deploy/README.md`](deploy/README.md)); migrations must have working `Up` **and** `Down`.
   - TS: `pnpm -r typecheck`.
   - **Open-core boundary:** the core (`services/ cli/ internal/ apps/ edge/`) must never
     import anything under `cloud/` or `ee/` except through a build-tagged DI seam. CI
     enforces this, so don't break it.
5. **Open a PR** describing the change and linking the issue. End the PR body with the
   standard generation footer if applicable.

## Migration rules

- The **`app`** schema is Go-owned via **goose** migrations in `db/migrations/app/`. Each
  file is `NNNN_name.sql` with both `-- +goose Up` and `-- +goose Down` sections.
- **Never hand-edit a production schema.** RLS policies, GRANTs, and triggers are
  hand-written, reviewed migrations.
- The **`identity`** schema belongs to Better Auth; do not migrate it here.
- The **`billing`** schema is cloud-only; the core must never reference it.
- New tenant tables **must** carry a denormalized `org_id`, a composite index leading on
  `org_id`, and `ENABLE` + `FORCE ROW LEVEL SECURITY` with a subquery-free
  `org_id = NULLIF(current_setting('app.current_org_id', true), '')::uuid` policy (the
  `NULLIF` coerces an unset/reset GUC to NULL → default-deny without a uuid-syntax error).

## Code of Conduct

Be respectful and constructive. Harassment or abuse is not tolerated.

## Licensing of contributions

Contributions to the core are accepted under [FSL-1.1-Apache-2.0](LICENSE). By submitting a
DCO-signed contribution you agree it is licensed under those terms.
