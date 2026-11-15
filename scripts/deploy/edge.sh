#!/usr/bin/env bash
# Deploy the Cloudflare serving Worker (edge/serving-worker) for a tested revision.
#
# A dry run first proves the bundle carries every binding production depends on.
# A missing RATE_LIMITER binding silently falls back to a KV write per request,
# which exhausts the account's daily KV write quota, so it blocks the deploy.
set -euo pipefail
: "${SOURCE_SHA:?Missing source revision}"
: "${CLOUDFLARE_API_TOKEN:?Set CLOUDFLARE_API_TOKEN (Workers Scripts edit) on the production GitHub environment}"
[[ "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]

WORKER_DIR=edge/serving-worker
EXPECTED_WORKER=dropway-production
REQUIRED_BINDINGS=(ROUTES LIMITS BUCKET RATE_LIMITER)

# The config name must be the live Worker: any other name makes wrangler create a
# second Worker and leave the one serving traffic unchanged.
name=$(sed -n 's/^name *= *"\([^"]*\)".*/\1/p' "$WORKER_DIR/wrangler.toml" | head -n 1)
if [[ "$name" != "$EXPECTED_WORKER" ]]; then
  echo "::error::wrangler.toml names the Worker '$name'; expected '$EXPECTED_WORKER'"
  exit 1
fi

cd "$WORKER_DIR"
dry_run=$(pnpm exec wrangler deploy --dry-run 2>&1) || { echo "$dry_run"; echo "::error::wrangler dry run failed"; exit 1; }
echo "$dry_run"
# Wrangler only warns, and then drops the block, when a config key is unknown.
if grep -q "Unexpected fields" <<<"$dry_run"; then
  echo "::error::wrangler.toml has fields wrangler ignores; their bindings would be missing"
  exit 1
fi
for binding in "${REQUIRED_BINDINGS[@]}"; do
  if ! grep -q "env\.${binding}\b" <<<"$dry_run"; then
    echo "::error::Dry run is missing the $binding binding; refusing to deploy"
    exit 1
  fi
done

pnpm exec wrangler deploy --tag "${SOURCE_SHA:0:12}" --message "Deploy ${SOURCE_SHA}"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "Deployed Cloudflare Worker \`$EXPECTED_WORKER\` at \`$SOURCE_SHA\`" >> "$GITHUB_STEP_SUMMARY"
fi
