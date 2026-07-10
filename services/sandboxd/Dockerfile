# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# The AI-builder SANDBOX image. Build context is the REPO ROOT:
#   docker build -f services/sandboxd/Dockerfile -t dropway-sandbox .
#
# This image runs UNTRUSTED, AI-generated build commands. Its entrypoint is
# sandboxd (the in-container agent the Dropway API drives). The image bundles a
# lean static-site toolchain (Node LTS + npm/pnpm, Python 3) so the model can run
# real builds (npm install, bundlers). It holds NO Dropway/R2/OpenRouter secrets:
# only a per-sandbox agent token (SANDBOXD_TOKEN) injected at boot.

FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /out/sandboxd ./services/sandboxd/cmd/sandboxd

# ---- Runtime: a real toolchain (NOT distroless — builds need a shell) --------
FROM node:22-bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-pip ca-certificates git \
    && corepack enable \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/sandboxd /usr/local/bin/sandboxd

# Non-root: the untrusted build never needs root, and dropping it shrinks the
# blast radius of a container escape.
RUN useradd -m -u 10001 builder && mkdir -p /workspace && chown builder:builder /workspace
USER builder:builder
WORKDIR /workspace

ENV SANDBOXD_PORT=8090 SANDBOXD_WORKDIR=/workspace
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/sandboxd"]
