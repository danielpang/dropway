---
name: verify
description: Run a Dropway change in the real stack and observe it working. Use before reporting any change as done when it alters behavior a dashboard user, API or SDK client, CLI user or site visitor would see. Not needed for docs-only or comment-only diffs.
---

# Verify a change end to end

Unit tests, typecheck and lint prove the code compiles and the pieces agree. This skill proves
the change works: you run the affected surface and watch the result.

## 1. Decide what to observe

Read the diff (`git diff main...HEAD` plus uncommitted changes) and write down, in one or two
sentences, what a person should now be able to do or see that they could not before. That
sentence is what you verify. If you cannot write it, ask the user what the change is for.

Then pick the surfaces the diff touches:

| Diff touches | Observe it by |
|---|---|
| `apps/dashboard` UI | Driving the page in a browser against the local stack |
| `services/api`, `internal/manifest`, deploy flow | `deploy/verify-dragdrop.sh` against the local stack |
| `services/serve` | Publishing a public site and fetching its live URL |
| `services/mcp` | `go test -tags integration ./services/mcp/cmd/mcp/...` (needs Postgres; see CI job `mcp`) |
| `edge/serving-worker` | `pnpm --filter @dropway/serving-worker test`; the Worker has no local stack |
| `packages/sdk`, `cli/` | `apps/dashboard/e2e/sdk-deploy.spec.ts`, or the built CLI against the local stack |
| `packages/room-runtime`, project rooms | The `room-*.spec.ts` Playwright specs |
| `db/migrations` | The `verify-migration` skill first, then this one |

## 2. Start the stack

Skip this step if only the edge Worker changed.

```sh
[ -f deploy/.env ] || cp deploy/.env.example deploy/.env
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
docker compose -f deploy/docker-compose.yml --env-file deploy/.env exec -T dashboard \
  pnpm dlx @better-auth/cli@latest migrate --yes
```

Retry the migrate command for up to two minutes; the dashboard container takes a while to
accept it. Then wait until `curl -s -o /dev/null -w '%{http_code}' http://localhost:3000/api/auth/ok`
prints `200`.

Rebuild (`up -d --build`) after every code change. A stale container verifies the old code.

If Docker is not available, stop here and report that the end-to-end check could not run. Do
not substitute unit tests and call it verified.

## 3. Run the observation

**Backend deploy path.** `deploy/verify-dragdrop.sh` signs up a user, creates an org and a
site, uploads `examples/synthwave-sunset`, finalizes, publishes and checks object-store CORS.
It prints a pass and fail count and exits non-zero on any failure.

**Dashboard UI.** First run the Playwright spec that covers the area, if one exists:

```sh
pnpm --filter @dropway/sdk build
pnpm --filter @dropway/dashboard exec playwright test e2e/<spec>.spec.ts --trace off
```

Then drive the changed screen yourself with Playwright against `http://localhost:3000`: sign
up, reach the screen, perform the action from step 1, and take a screenshot of the result.
Try at least one edge case the change could break (empty state, a long name, a second org, a
narrow viewport). If the change adds behavior no spec covers, add or extend a spec in
`apps/dashboard/e2e/`.

**Served sites.** Gated sites (`org_only`, `allowlist`, `password`) need https and return a
redirect over local http. To see served content locally, publish a `public` site from an org
with external sharing allowed and fetch `http://<org>--<site>.localhost:8090/`.

## 4. Fix and repeat

If the observation fails, fix the cause, rebuild, and run the same observation again. Do not
weaken or skip a check to get a pass. Stop and ask the user if the failure is in code outside
the change or you have tried three fixes without progress.

## 5. Report

State, in this order:

1. What you verified (the sentence from step 1).
2. What you ran and what you saw: commands, pass counts, screenshot paths.
3. What you did not verify and why.

Leave the stack running if the user is likely to look at it; otherwise stop it with
`docker compose -f deploy/docker-compose.yml --env-file deploy/.env down`. Never pass `-v`
unless the user asks, because that deletes their local database and blobs.
