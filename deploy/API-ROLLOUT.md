# API schema rollout

Apply the app migrations before releasing the new API image. Disabling project rooms or hosted execution does not remove the API's schema dependencies: API-key authentication, version retention and projection recovery also use the new schema.

The collaboration PRs form a stack. Promote the final tested revision containing the rollout fixes; do not deploy intermediate stack revisions. If an external service auto-deploys every main-branch merge, hold its production promotion while landing the stack. This repository does not control external deployment integrations.

1. Merge the migrations and wait for the `migrate` workflow's development stage to succeed. Approve its production environment stage and verify that it succeeds for the same revision. This workflow owns production DDL; the API runtime does not.
2. Build the API image and run its read-only preflight with the target database's **runtime** connection:

   ```sh
   docker run --rm --env DATABASE_URL dropway-api:release --check-schema
   ```

   Set `DATABASE_URL` in the invoking environment. Do not put credentials in the command line or substitute an owner connection to make a permission failure pass. The command needs only this database configuration, exits nonzero for missing schema or runtime permissions, and does not start HTTP, telemetry, background jobs or migrations.
3. Deploy the API with the repository's `fly.toml`. Its `release_command = "--check-schema"` runs the same check in the new image before Fly updates serving Machines. Do not bypass the release command. If it fails, complete the migration or fix runtime grants, then retry the deployment; leave the serving release in place.
4. Check the rollout's health checks and existing API-key/site workflows before enabling project rooms or hosted execution for any organization. Hosted execution has separate development verification gates.

The API also checks the schema on ordinary startup before accepting traffic, including when deployed outside Fly. Other platforms should run the image preflight as a deployment prerequisite, then use readiness checks to keep existing instances until replacements are healthy. A process startup check alone cannot preserve availability if an operator stops every old instance first.

The preflight checks required objects, result shapes and runtime access, rather than trusting a migration version number. The runtime role does not need access to Goose's owner-managed bookkeeping table. A successful check verifies schema readiness; it does not validate provider credentials or perform live model/Sprite calls.

For an application rollback, retain the expanded schema and redeploy the previous compatible API image. The previous API-key resolver explicitly selects its original seven columns, so adding `scopes` and `site_id` does not break it. Do not run destructive Goose down migrations while either release is serving; they remove review and execution records. Migration 0022 adds a bounded pending-projection lookup without replacing the old API's functions.

Fly's release command replaces Docker `CMD`, **not** `ENTRYPOINT`; this image already sets `ENTRYPOINT ["/usr/local/bin/api"]`, so the flag alone is intentional. Fly runs the check in a temporary Machine with the app environment and secrets, and aborts deployment on a nonzero exit. See the [official Fly deployment configuration](https://fly.io/docs/reference/configuration/#the-deploy-section).
