#!/usr/bin/env bash
# Apply all schema owners in dependency order. Never run as the runtime role.
set -euo pipefail
: "${APP_DSN:?Missing application database owner URL}"
: "${BILLING_DSN:?Missing billing database owner URL}"

goose -dir db/migrations/app postgres "$APP_DSN" up
goose -table goose_db_version_billing -dir db/migrations/billing postgres "$BILLING_DSN" up
# app/0011+0012 establish identity search_path and default grants first.
(
  cd apps/dashboard
  BETTER_AUTH_DATABASE_URL="$APP_DSN" \
    BETTER_AUTH_SECRET=migrate-only-placeholder \
    pnpm dlx @better-auth/cli@latest migrate --yes
)
