#!/usr/bin/env bash
# Deploy only the immutable images built by this workflow run, API before clients.
set -euo pipefail
: "${DEPLOY_ENVIRONMENT:?Missing environment}"
: "${SOURCE_SHA:?Missing source revision}"
: "${FLY_API_APP:?Missing API app target}"
: "${FLY_MCP_APP:?Missing MCP app target}"
: "${FLY_API_TOKEN:?Missing API deploy token}"
: "${FLY_MCP_API_TOKEN:?Missing MCP deploy token}"
[[ "$DEPLOY_ENVIRONMENT" == development || "$DEPLOY_ENVIRONMENT" == production ]]
# Production always includes the worker; development may opt in separately.
if [[ "$DEPLOY_ENVIRONMENT" == production ]]; then
  FLY_WORKER_APP=dropway-worker-production
fi
[[ "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$FLY_API_APP" != "$FLY_MCP_APP" ]]
if [[ -n "${FLY_WORKER_APP:-}" ]]; then
  : "${FLY_WORKER_API_TOKEN:?Missing worker deploy token}"
  [[ "$FLY_WORKER_APP" != "$FLY_API_APP" && "$FLY_WORKER_APP" != "$FLY_MCP_APP" ]]
fi

validate_image() {
  local service="$1" app="$2"
  [[ "$app" =~ ^[a-z0-9][a-z0-9-]*$ ]] || return 1
  local local_image="dropway-${service}:${SOURCE_SHA}"
  # Validate the artifact's image IDs before pushing anything to the registry.
  local expected actual
  expected=$(python3 -c 'import json,sys; print(json.load(open("image-ids.json"))[sys.argv[1]])' "$service")
  actual=$(docker image inspect --format '{{.Id}}' "$local_image")
  [[ "$actual" == "$expected" ]] || { echo "::error::Image identity mismatch for $service"; return 1; }
}

validate_image api "$FLY_API_APP"
validate_image mcp "$FLY_MCP_APP"
if [[ -n "${FLY_WORKER_APP:-}" ]]; then validate_image room-worker "$FLY_WORKER_APP"; fi

ship() {
  local service="$1" app="$2" config="$3" token="$4" healthcheck="$5"
  local local_image="dropway-${service}:${SOURCE_SHA}" remote="registry.fly.io/${app}:deploy-${SOURCE_SHA}"
  export FLY_API_TOKEN="$token"
  flyctl auth docker
  docker tag "$local_image" "$remote"
  docker push "$remote"
  # Use the pushed digest, never a mutable tag, for the actual release.
  local digest
  digest=$(docker image inspect --format '{{json .RepoDigests}}' "$remote" |
    python3 -c 'import json,sys; prefix=sys.argv[1]+"@sha256:"; print(next(x for x in json.load(sys.stdin) if x.startswith(prefix)))' "registry.fly.io/$app")
  flyctl deploy --app "$app" --config "$config" --image "$digest" \
    --env "ENVIRONMENT=$DEPLOY_ENVIRONMENT" --strategy rolling --wait-timeout 5m --yes
  # flyctl waits for rollout health checks. Also check each public API directly.
  if [[ "$healthcheck" != none ]]; then
    curl --fail --silent --show-error --retry 10 --retry-all-errors --retry-delay 3 \
      --connect-timeout 10 --max-time 20 "https://${app}.fly.dev/healthz" >/dev/null
  fi
  printf '%s: %s\n' "$app" "$digest" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
}

# Save tokens: ship sets FLY_API_TOKEN for each separate app-scoped login.
api_token="$FLY_API_TOKEN"
ship api "$FLY_API_APP" fly.toml "$api_token" public
ship mcp "$FLY_MCP_APP" fly.mcp.toml "$FLY_MCP_API_TOKEN" public
if [[ -n "${FLY_WORKER_APP:-}" ]]; then
  ship room-worker "$FLY_WORKER_APP" fly.room-worker.toml "$FLY_WORKER_API_TOKEN" none
fi
