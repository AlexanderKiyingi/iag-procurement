# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY pkg/authclient ./pkg/authclient
RUN go mod download
COPY . .

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

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/

ENV PORT=4009 \
    AUTO_MIGRATE=true \
    GIN_MODE=release \
    ENVIRONMENT=production \
    AUTH_MODE=gateway

EXPOSE 4009
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/server"]
