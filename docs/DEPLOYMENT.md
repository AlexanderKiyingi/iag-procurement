# Procurement service — deployment

## Overview

| Item | Value |
|------|--------|
| Port | `4009` (default) |
| Health | `GET /health`, `GET /healthz` |
| Readiness | `GET /ready` (Postgres + Redis) |
| API prefix | `/api/v1` |
| Auth | Platform JWT via gateway (`AUTH_MODE=gateway`); see [PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md) |
| Events | Consumes `pm.requisition.submitted` on `iag.commercial` when `EVENT_BUS_ENABLED=true` |

## Docker

```bash
docker build -t iag/procurement:latest .
docker run --rm -p 4009:4009 \
  -e DATABASE_URL=postgres://... \
  -e REDIS_URL=redis://... \
  -e AUTH_MODE=gateway \
  -e GATEWAY_INTERNAL_SECRET=your-16-char-min-secret \
  -e JWT_ISSUER=http://authentication:3001 \
  -e JWKS_URL=http://authentication:3001/.well-known/jwks.json \
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
