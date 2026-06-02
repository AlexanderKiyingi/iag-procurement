# syntax=docker/dockerfile:1.7
#
# Targets:
#   standalone (default) — iag-procurement repo root on Railway
#   monorepo             — IAG_multi_backend root context (deploy/docker-compose)
#
# Monorepo:   docker build -f services/operations/procurement/Dockerfile --target monorepo .
# Standalone: docker build --target standalone .

FROM golang:1.23-alpine AS base
RUN apk add --no-cache git ca-certificates
ENV PLATFORM_GO_DEP=/deps/platform-go

FROM base AS platform-go-copy
COPY shared/platform-go ${PLATFORM_GO_DEP}

FROM base AS build-standalone
# Standalone (iag-procurement repo root): the meta-repo is private, so
# Railway can't clone it at build time. Instead the standalone repo carries a
# committed snapshot at third_party/platform-go (refreshed via
# scripts/sync-platform-go.sh). Copy that into /deps/platform-go and point the
# replace directive at it.
WORKDIR /src
COPY third_party/platform-go ${PLATFORM_GO_DEP}
COPY go.mod go.sum ./
COPY pkg/authclient ./pkg/authclient
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY . .
ARG VERSION=dev
# `COPY . .` restored go.mod from the build context, which still carries the
# meta-repo-only `replace => ../../../shared/platform-go`. That path does not
# exist inside the build container, so re-apply the vendored replace before
# build.
RUN set -eu; \
    go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP}; \
    mkdir -p /out; \
    for cmd in ./cmd/server ./cmd/healthcheck; do \
        name=$(basename "$cmd"); \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM base AS build-monorepo
COPY --from=platform-go-copy ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src/services/operations/procurement
COPY services/operations/procurement/go.mod services/operations/procurement/go.sum ./
COPY services/operations/procurement/pkg/authclient ./pkg/authclient
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY services/operations/procurement/ .
ARG VERSION=dev
RUN set -eu; \
    mkdir -p /out; \
    for cmd in ./cmd/server ./cmd/healthcheck; do \
        name=$(basename "$cmd"); \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS monorepo
WORKDIR /app
COPY --from=build-monorepo /out/ /app/
ENV PORT=4009 \
    AUTO_MIGRATE=true \
    GIN_MODE=release \
    ENVIRONMENT=production \
    AUTH_MODE=gateway
EXPOSE 4009
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/server"]

FROM gcr.io/distroless/static-debian12:nonroot AS standalone
WORKDIR /app
COPY --from=build-standalone /out/ /app/
ENV PORT=4009 \
    AUTO_MIGRATE=true \
    GIN_MODE=release \
    ENVIRONMENT=production \
    AUTH_MODE=gateway
EXPOSE 4009
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/server"]
