# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Caddy edge for the Dropway self-host stack — the open-source replacement for
# Cloudflare-for-SaaS (per-tenant custom-hostname TLS) + the edge cache. This image
# is stock Caddy 2 plus the Souin response-cache module (cache-handler) built in via
# xcaddy. It fronts the `serve` content server with automatic HTTPS, ON-DEMAND TLS
# for arbitrary tenant custom domains (gated by serve's /tls-check ask endpoint), and
# Cache-Control-honoring response caching.
#
# The Caddyfile is BIND-MOUNTED via docker-compose (so operators can edit it without
# rebuilding), so it is deliberately NOT copied into the image here.
#
# Build:  docker build -f deploy/caddy/Dockerfile -t dropway-caddy deploy/caddy

# ---- Stage 1: build a custom caddy binary with the cache-handler module. --------
FROM caddy:2-builder AS builder

# Souin's Caddy module — HTTP response cache that honors origin Cache-Control
# (skips private / no-store / Set-Cookie responses, so gated content is never cached).
RUN xcaddy build \
    --with github.com/caddyserver/cache-handler

# ---- Stage 2: the runtime image = stock caddy:2 + our custom binary. -----------
FROM caddy:2

# Replace the stock caddy binary with the cache-handler-enabled build.
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
