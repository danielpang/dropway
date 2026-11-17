---
name: ship
description: Run Dropway's full pre-pull-request routine on the current branch - review, cleanup, static checks, the path-specific verification skills and an end-to-end check. Use when the user asks to ship, finish, wrap up or open a pull request for a change.
---

# Ship a change

This skill chains the other checks so a branch reaches review already verified. Run the steps
in order. When a step finds a problem, fix it and rerun that step before moving on; a fix
that changes code means the earlier static checks run again at the end.

## 1. Review the diff

Invoke `/code-review` on the current branch. Fix the findings you agree with. For any you
reject, keep a one-line reason for the final report.

## 2. Simplify

Invoke `/simplify` on the diff and keep the cleanups that do not change behavior.

## 3. Static checks

Run the commands from `CLAUDE.md` for each area the diff touches:

- Go files: build, vet, test, and the `-tags cloud` build.
- TypeScript: build the SDK, then typecheck, lint and the unit tests of each changed package.
- `scripts/deploy/` or `.github/workflows/`: shellcheck, the deploy script tests, actionlint.

## 4. Path-specific skills

Check `git diff --name-only main...HEAD` and invoke each skill whose paths appear:

| Paths in the diff | Skill |
|---|---|
| `db/migrations/**`, `db/sqlc/schema.sql` | `verify-migration` |
| `services/api/openapi/**`, `db/sqlc/**`, generated outputs | `verify-api-contract` |

Also confirm by hand that nothing under `services/`, `cli/`, `internal/`, `apps/` or `edge/`
gained an import of `cloud/` or `ee/` outside a build-tagged seam.

## 5. End-to-end

Invoke the `verify` skill. Skip it only when the diff cannot change runtime behavior (docs,
comments, CI-only changes), and say that you skipped it and why.

## 6. Commit and report

- Commit with sign-off (`git commit -s`); every commit needs a `Signed-off-by` trailer.
- Do not push or open the pull request unless the user asked for that.
- Report one line per step: what ran and the result. List anything that did not run, any
  review findings you declined, and anything the user must decide.

Never mark a step as passed if it did not run. Never skip, disable or loosen a test to get a
green result.
