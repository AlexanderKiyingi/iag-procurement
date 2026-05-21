# iag-procurement

Procurement microservice for the IAG platform (Go/Gin).

Integrates with **iag-authentication** (gateway JWT / RBAC), **iag-notifications**, and **iag-project-management** (requisition import). See [docs/PLATFORM_INTEGRATION.md](docs/PLATFORM_INTEGRATION.md).

Registry: [`subrepos.json`](../../../subrepos.json) · Dev port: **4009**

## Run locally

```bash
cp .env.example .env
# set DATABASE_URL, AUTH_MODE=gateway, GATEWAY_INTERNAL_SECRET, JWKS_URL, etc.

go run ./cmd/server
```

Or from the meta-repo: `pnpm dev:procurement` (after adding script) / Docker Compose `procurement` service.
