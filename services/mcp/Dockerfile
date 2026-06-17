# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for the Dropway MCP server (services/mcp/cmd/mcp).
# Build context is the REPO ROOT (the Go module spans the whole repo):
#   docker build -f services/mcp/Dockerfile -t dropway-mcp .
#
# The MCP server is an OAuth-protected, remote (Streamable HTTP) Model Context
# Protocol endpoint that lets an authorized LLM agent list + read a tenant's
# deployed sites — including gated content — scoped to one org by Postgres RLS.
# It verifies Better-Auth-issued OAuth access tokens against the platform JWKS and
# re-checks the org-level mcp_enabled switch per request.

FROM golang:1.25 AS build
WORKDIR /src

# Cache deps separately from sources.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off → a static binary that runs on distroless.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w" \
      -o /out/mcp ./services/mcp/cmd/mcp

# ---- Runtime: distroless static, non-root ----------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mcp /usr/local/bin/mcp
EXPOSE 8092
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mcp"]
