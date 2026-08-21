# Procurement — platform integration

## API gateway

| Public URL | Upstream |
|------------|----------|
| `http://localhost:8080/api/v1/procurement/*` | `http://procurement:4009/*` |

Examples:

- Health: `GET /api/v1/procurement/ready`
- Me: `GET /api/v1/procurement/api/v1/auth/me` (platform Bearer token)
- Data: `GET /api/v1/procurement/api/v1/requisitions`

The gateway verifies **platform** JWTs (`aud=iag.gateway`) and forwards the `Authorization: Bearer` header unchanged. Procurement runs with `AUTH_MODE=jwt` (default), re-verifies the token locally via JWKS (`aud=iag.procurement`), and enforces permissions from JWT claims (codenames seeded in **iag-authentication**, e.g. `procurement.view_seed`).

> **Removed:** `AUTH_MODE=gateway` and `GATEWAY_INTERNAL_SECRET` / `X-IAG-*` header trust. Production always uses Bearer JWT + JWKS.

Compose: `UPSTREAM_PROCUREMENT=http://procurement:4009` on `api-gateway`.

## Authentication

| Component | Role |
|-----------|------|
| **iag-authentication** | Users, groups (`procurement-admin`, `procurement-member`, `procurement-viewer`), `procurement.*` permissions |
| **api-gateway** | OAuth2 / JWT validation (`aud=iag.gateway`) → forwards Bearer token |
| **procurement** | Re-verifies Bearer JWT via JWKS (`AUTH_MODE=jwt`, `aud=iag.procurement`) |

### Auth modes

| `AUTH_MODE` | Use |
|-------------|-----|
| `jwt` | Production and local dev with Bearer token + JWKS (default) |
| `legacy` | Local-only HS256 login at `POST /api/v1/auth/login` and in-service RBAC admin |

### Env (production / jwt)

```env
AUTH_MODE=jwt
JWT_ISSUER=http://authentication:3001
JWKS_URL=http://authentication:3001/.well-known/jwks.json
AUDIENCE=iag.procurement
SERVICE_CLIENT_ID=iag-procurement
SERVICE_CLIENT_SECRET=...
```

Clients obtain a token from authentication (`POST /oauth/token` or gateway login flow), then call procurement via the gateway with `Authorization: Bearer <platform token>`. The JWT must include `aud=iag.procurement` (in addition to `iag.gateway` for the gateway hop).

Assign users to `procurement-member` or `procurement-admin` in authentication for mutate access.

## Event bus — PM requisitions

| Direction | Topic | Type |
|-----------|-------|------|
| Consume | `iag.commercial` | `pm.requisition.submitted` |

When project management publishes a requisition, procurement imports it into `requisitions` (column `pm_requisition_id`, unique). Duplicate PM ids are ignored. A `requisition.pending` in-app notification is emitted via the signals bus.

### PM event payload (`data`)

| Field | Description |
|-------|-------------|
| `requisitionId` | PM workspace numeric id (string) |
| `title` | Requisition title |
| `amount` | Decimal string |
| `currency` | e.g. `USD` |
| `status` | PM status (`submitted` → Pending Approval) |
| `requestedBy` | Workspace actor initials |
| `forDept` | Department |
| `urgency` | Maps to priority |
| `payee`, `justification` | Stored in audit detail |

### Env (consumer)

```env
EVENT_BUS_ENABLED=true
KAFKA_BROKERS=redpanda:9092
KAFKA_COMMERCIAL_TOPIC=iag.commercial
KAFKA_CONSUMER_GROUP=iag.procurement.commercial
```

## Event bus — outbound (transactional outbox → `iag.commercial`)

Emitted atomically with the state change via the procurement outbox; consumed by iag-finance (GL/AP, GR/IR) and iag-warehouse (goods receipt).

| Event type | When | Key `data` fields |
|------------|------|-------------------|
| `procurement.requisition.approved` / `.rejected` | Requisition reaches a terminal decision | `requisitionId`, `procurementRequisitionId`, `budgetId`, `approvedBy`/`rejectedBy`, `approvedAt`/`rejectedAt` |
| `procurement.invoice.received` | Vendor invoice captured | `documentRef`, `vendorRef`, `amount`, `currency`, `dueDate`, **`poRef`** (PO id — lets finance clear the GR/IR accrual), `description` |
| `procurement.grn.posted` | Goods receipt posted | `grn_id`, `po_id`, `vendor_id`, `received_by`, **`amount`** (received value = Σ `grn_lines` qty×unit_price), **`inventory_value`** (the `items.stockable` portion of `amount` — finance capitalises it and expenses the rest), `lines[]` (sku/qty/uom/**unit_price** for warehouse intake and its weighted-average costing) |

> This is the **only** accounting event for a PO delivery. The
> `warehouse.movement.posted` raised for the same goods is stamped
> `source_doc_type: "procurement_grn"` and books nothing — both posting credited
> GR/IR twice for one receipt. See iag-finance `docs/EVENT_CONTRACT.md` §2.1.
> Mark service and direct-to-site items `stockable = FALSE` so their receipts
> expense rather than capitalise; the column defaults to TRUE.

`poRef` on the invoice and `amount` on the GRN drive the **GR/IR clearing** flow in finance: the GRN accrues `Dr expense / Cr GR-IR`, and the PO-referenced invoice later clears it (`Dr GR-IR / Cr AP`) instead of double-booking the expense.

> **Tiered requisition approval** (amount-band, distinct approvers) is enforced in-service when `PROCUREMENT_REQUIRE_TIERED_APPROVAL=true` via `POST /requisitions/:id/approve|reject`; it does not change the outbound event shape — the approved event still fires once the final tier signs.

## Local dev

```bash
pnpm infra:up
curl -fsS http://localhost:4009/ready
curl -fsS http://localhost:8080/api/v1/procurement/ready
```

Token via authentication, then:

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/procurement/api/v1/auth/me
```
