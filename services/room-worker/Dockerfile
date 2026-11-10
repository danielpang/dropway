FROM node:24-bookworm-slim AS build
RUN corepack enable && corepack prepare pnpm@9.12.0 --activate
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY packages/room-runtime/package.json packages/room-runtime/package.json
RUN pnpm install --frozen-lockfile --filter @dropway/room-runtime...
COPY packages/room-runtime packages/room-runtime
RUN pnpm --filter @dropway/room-runtime build
RUN pnpm --filter @dropway/room-runtime deploy --prod /worker

FROM node:24-bookworm-slim
ENV NODE_ENV=production
WORKDIR /worker
COPY --from=build --chown=node:node /worker ./
USER node
CMD ["node", "dist/daemon.js"]
