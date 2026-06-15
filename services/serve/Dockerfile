# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for the Shipped self-host content server (services/serve/cmd/serve).
# Build context is the REPO ROOT (the Go module spans the whole repo):
#   docker build -f services/serve/Dockerfile -t shipped-serve .
#
# This is the plain Go alternative to the Cloudflare serving Worker: it serves
# tenant static content on *.shippedusercontent.com + custom domains with no
# Cloudflare. OSS/self-host only (no build tags). Front it with Caddy (deploy/caddy)
# for automatic HTTPS + on-demand TLS for custom domains.

FROM golang:1.25 AS build
WORKDIR /src

# Cache deps separately from sources.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off → a static binary that runs on distroless.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /out/serve ./services/serve/cmd/serve

# ---- Runtime: distroless static, non-root ----------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/serve /usr/local/bin/serve
EXPOSE 8090 8091
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/serve"]
