# Hosted deployment and schema rollout

`Deploy hosted` owns normal Fly releases. A successful `ci` run for a push to main starts this sequence for that exact commit:

1. Migrate development: app, billing, then Better Auth identity.
2. Build the cloud API, MCP and room-worker images once; deploy development API and MCP, then the worker when configured.
3. After development rollout and HTTP health checks succeed, migrate production in the same order.
4. Promote the same image artifact to production; deploy API, MCP, then the optional worker.

Every dependency must succeed before the next stage starts. A single concurrency group serializes complete rollouts and manual migration recovery. GitHub may replace a pending run with a newer pending run; migrations are cumulative. Delayed automatic CI completions for commits superseded on main are ignored. Manual deployments must run from main and default to development only; selecting production still passes through development first. Manual dispatch is an operator recovery path and does not wait for a separate CI run.

Images are checked against their recorded Docker image IDs, pushed to each app's Fly registry, then released by digest. Production does not rebuild. Artifacts expire after three days; start a new complete rollout if they have expired. The API retains its read-only `--check-schema` Fly release command before replacing serving Machines. HTTP readiness does not replace product acceptance testing.

## Configuration and cutover

This PR does not provision apps, change runtime secrets, or disable external deployment integrations. Before enabling the new pipeline:

1. Provision distinct development API/MCP apps with development database, storage and application credentials. Do not point development apps at production data. Optionally provision a dedicated worker per environment.
2. Set the following in each GitHub environment (`development` and `production`):

   | Kind | Name | Value |
   | --- | --- | --- |
   | Variable | `FLY_API_APP` | That environment's API app |
   | Variable | `FLY_MCP_APP` | That environment's MCP app |
   | Variable | `FLY_WORKER_APP` | Optional worker app; omit to skip deployment |
   | Secret | `FLY_API_TOKEN` | App-scoped API deployment token |
   | Secret | `FLY_MCP_API_TOKEN` | App-scoped MCP deployment token |
   | Secret | `FLY_WORKER_API_TOKEN` | Required when worker is configured |

   Production apps currently are `dropway-production` and `dropway-mcp-prod`. No development API/MCP pair is currently configured. Runtime secrets remain in Fly; this workflow supplies only `ENVIRONMENT` and the image/config.

3. Retain development's `DEV_DATABASE_OWNER_URL` / `DEV_BILLING_OWNER_URL` and production's `PROD_DATABASE_OWNER_URL` / `PROD_BILLING_OWNER_URL`. These are privileged migration connections, not runtime connections. App migrations run first so grants and identity search paths exist; billing uses its separate `goose_db_version_billing` ledger. Better Auth runs last.
4. Configure production environment required reviewers if a human promotion gate is desired. Environment names alone do not provide approval protection; neither environment currently has required reviewers.
5. Disable Fly's GitHub automatic deployments for each affected app before the first main merge handled by this workflow. Otherwise Fly can still race the migration jobs. Coordinate that switch with merging this PR and verify the first development run before requesting production.
6. Vercel dashboard and Cloudflare deployments are **not** controlled by this Fly workflow. Their automatic deployment integrations must be handled separately to guarantee ordering for those services too. In particular, dashboard auto-deployment can still race schema changes until it is moved behind the migration dependency.

Missing deployment credentials fail before the corresponding environment's migration. The old `migrate` workflow is manual-only recovery, so it cannot independently advance production while development services are still deploying. Do not merge this replacement before configuring its environment prerequisites and coordinating external auto-deploy cutover.

## Failure recovery and compatibility

Migrations must remain compatible with the currently serving release: migration-first deployment necessarily leaves old code running against the expanded schema during rollout. Apply additive changes first; remove old schema only in a later release after old code no longer uses it.

A failed deployment does not roll back schema changes. Fix the failure and rerun the pipeline, or redeploy a known compatible image. Do not run destructive Goose down migrations while either release is serving. For migration-only recovery, manually run `migrate` on main for one explicit environment; it shares the rollout lock.

The API release check uses the runtime database role and verifies schema objects, query shapes and permissions without running DDL. Do not bypass it or replace runtime credentials with owner credentials. Fly aborts the release if it fails. Existing serving Machines remain on their previous release.

Collaboration's PostHog flag is independent of schema readiness. Disabling it does not remove schema requirements. Continue provider/Sprite acceptance testing before expanding feature access; see [hosted collaboration configuration](../docs/hosted-collaboration-development.md).
