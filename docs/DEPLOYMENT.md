# Procurement service — deployment

## Overview

| Item | Value |
|------|--------|
| Port | `4009` (default) |
| Health | `GET /health`, `GET /healthz` |
| Readiness | `GET /ready` (Postgres + Redis) |
| API prefix | `/api/v1` |
| Auth | Platform Bearer JWT (`AUTH_MODE=jwt`, `aud=iag.procurement`); see [PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md) |
| Events | Consumes `pm.requisition.submitted` on `iag.commercial` when `EVENT_BUS_ENABLED=true` |

## Railway

Connect the Railway service to the **`iag-procurement`** standalone repo (not `IAG_multi_backend` — procurement is a git submodule there and Railway does not init submodules by default).

| Setting | Value |
|---------|--------|
| Repository | `AlexanderKiyingi/iag-procurement` |
| Root directory | `/` (repo root) |
| Dockerfile | `Dockerfile` (default `standalone` target) |
| `PORT` | `4009` |

Set production env from [config/.env.production.example](../config/.env.production.example): `DATABASE_URL`, `REDIS_URL`, `AUTH_MODE=jwt`, `JWT_ISSUER`, `JWKS_URL`, `AUDIENCE=iag.procurement`, and `SERVICE_CLIENT_*` for permission registration.

If you must deploy from the meta-repo instead, use build context `.`, Dockerfile `services/operations/procurement/Dockerfile`, target `monorepo`, and run `git submodule update --init services/operations/procurement` before the Docker build.

## Docker

Standalone (Railway / iag-procurement repo root) uses the committed
`third_party/platform-go` snapshot. Refresh before deploy when platform-go changes:

```bash
sh scripts/sync-platform-go.sh
git add third_party/platform-go
```

```bash
docker build --target standalone -t iag/procurement:latest .
docker run --rm -p 4009:4009 \
  -e DATABASE_URL=postgres://... \
  -e REDIS_URL=redis://... \
  -e AUTH_MODE=jwt \
  -e JWT_ISSUER=http://authentication:3001 \
  -e JWKS_URL=http://authentication:3001/.well-known/jwks.json \
  -e AUDIENCE=iag.procurement \
  iag/procurement:latest
```

## Platform compose

From repo root:

```bash
docker compose -f deploy/docker-compose.yml up -d procurement
```

Uses shared Postgres (`iag_procurement` database) and Redis DB `3`.

## First-time bootstrap

1. Migrations run automatically when `AUTO_MIGRATE=true`.
2. Assign procurement groups in **iag-authentication** (`procurement-admin`, `procurement-member`, `procurement-viewer`).
3. For **legacy** local JWT only (`AUTH_MODE=legacy`), set `SEED_ON_STARTUP=true` to bootstrap `admin@iag.local` (see [SEED_DEMO_ACCOUNTS.md](./SEED_DEMO_ACCOUNTS.md)).

## Environment reference

See [.env.example](../.env.example) and [config/.env.production.example](../config/.env.production.example).
