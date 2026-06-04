# iag-procurement

Vendor master data, purchase orders, requisitions, and RFQs for the IAG platform — with supplier portal and SCM party sync.

| Field | Value |
|-------|-------|
| **Port** | `4009` |
| **Gateway prefix** | `/api/v1/procurement` |
| **Audience** | `iag.procurement` |
| **Remote** | [iag-procurement](https://github.com/AlexanderKiyingi/iag-procurement) |

## Role

Canonical **vendor procurement** service: vendors, POs, invoices, contracts, and PM requisition import. Links vendors to platform **`party_id`** via Kafka (`scm.party.*`) for cross-service supplier identity. Exposes **vendor portal** routes scoped to the authenticated user's linked vendor profile.

## Quick start

```bash
cd services/operations/procurement
cp .env.example .env
# DATABASE_URL, AUTH_MODE=jwt, JWKS_URL, AUDIENCE=iag.procurement

go run ./cmd/server
curl http://localhost:4009/health
```

From the meta-repo: `docker compose -f deploy/docker-compose.yml up procurement`

## Portal API (vendor JWT)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/portal/me` | Linked vendor profile + `party_id` |
| GET | `/portal/purchase-orders` | Own POs only |
| GET | `/portal/invoices` | Own invoices only |

Requires `platform_user_id` on the vendor row (set during supplier invite/onboarding).

## Event bus

| Direction | Topic | Purpose |
|-----------|-------|---------|
| Consume | `iag.commercial` | PM requisition import (`pm.requisition.submitted`) |
| Consume | `iag.supply-chain` | Party sync (`scm.party.created`, `scm.party.updated`) |

Party sync updates `vendors.party_id` and `vendors.scm_business_id` for vendor/cooperative parties registered in SCM.

## Integration

- **Auth:** `iag-authentication` (JWT, RBAC groups)
- **Notifications:** `iag-notifications` (optional dispatch)
- **PM:** `iag-project-management` requisition events
- **SCM / finance:** shared `party_id` for supplier portal across services

See [docs/PLATFORM_INTEGRATION.md](docs/PLATFORM_INTEGRATION.md).

Registry: [`subrepos.json`](../../../subrepos.json)
