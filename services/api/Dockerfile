# SPDX-License-Identifier: FSL-1.1-Apache-2.0
#
# Multi-stage build for the Shipped Go control-plane API (services/api/cmd/api).
# Build context is the REPO ROOT (the Go module spans the whole repo):
#   docker build -f services/api/Dockerfile -t shipped-api .
#
# Default = the OSS / self-host build (quota.Unlimited, no Stripe, no cloud/ee).
# For the hosted-cloud build pass:  --build-arg SHIPPED_BUILD_TAGS=cloud
# (links cloud/quota + cloud/billing — requires the Stripe env at runtime).

FROM golang:1.25 AS build
WORKDIR /src

# Cache deps separately from sources.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build tags select the open-core flavor (empty = OSS). CGO off → a static binary
# that runs on distroless.
ARG SHIPPED_BUILD_TAGS=""
RUN CGO_ENABLED=0 GOOS=linux go build \
      -tags "${SHIPPED_BUILD_TAGS}" \
      -ldflags="-s -w" \
      -o /out/api ./services/api/cmd/api

# ---- Runtime: distroless static, non-root ----------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/api"]
