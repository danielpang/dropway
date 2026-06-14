# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for the Shipped dashboard (Next.js, apps/dashboard).
# Build context is the REPO ROOT (it's a pnpm workspace):
#   docker build -f apps/dashboard/Dockerfile -t shipped-dashboard .
#
# The dashboard talks to the Go API for all business data and to Postgres only
# for Better Auth's own identity tables (DATABASE_URL). It runs `next start`.

# ---- deps: install the workspace (frozen lockfile) -------------------------
FROM node:22-slim AS deps
RUN corepack enable
WORKDIR /repo
# Workspace manifests first so the install layer caches across source changes.
# Every workspace member's package.json must be present for a frozen install.
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml tsconfig.base.json ./
COPY apps/dashboard/package.json apps/dashboard/package.json
COPY edge/serving-worker/package.json edge/serving-worker/package.json
COPY contracts/package.json contracts/package.json
COPY packages/tsconfig/package.json packages/tsconfig/package.json
COPY packages/eslint-config/package.json packages/eslint-config/package.json
# Not --frozen: the self-host quickstart should build even if pnpm-lock.yaml drifts
# slightly from package.json. (CI uses a frozen install for reproducibility.)
RUN pnpm install --no-frozen-lockfile --filter @shipped/dashboard...

# ---- build: next build -----------------------------------------------------
FROM node:22-slim AS build
RUN corepack enable
WORKDIR /repo
COPY --from=deps /repo /repo
COPY apps/dashboard/ apps/dashboard/
COPY contracts/ contracts/
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1
# Build-only placeholders (passed inline, NOT baked into the image): Better Auth
# initializes at import, but `next build` never connects to a DB (auth pages are
# dynamic via headers()). Real values are injected at runtime by Compose.
RUN DATABASE_URL=postgres://build:build@localhost:5432/build?sslmode=disable \
    BETTER_AUTH_SECRET=build-time-placeholder-not-used-at-runtime \
    BETTER_AUTH_URL=http://localhost:3000 \
    NEXT_PUBLIC_API_URL=http://localhost:8080 \
    pnpm --filter @shipped/dashboard build

# ---- run: next start -------------------------------------------------------
FROM node:22-slim AS run
RUN corepack enable
WORKDIR /repo/apps/dashboard
COPY --from=build /repo /repo
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0
EXPOSE 3000
# `next start` serves the production build; runtime env (DATABASE_URL,
# BETTER_AUTH_*, GOOGLE_*, NEXT_PUBLIC_API_URL) comes from Compose's env_file.
CMD ["pnpm", "exec", "next", "start", "-H", "0.0.0.0", "-p", "3000"]
