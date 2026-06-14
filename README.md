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

## Procurement controls

- **PO approval workflow.** POs are created `Pending Approval` (or `Approved` when the total is below `PROCUREMENT_APPROVAL_THRESHOLD`). Approve/reject via `PATCH /purchase-orders/:id` with a `status`. Goods can only be received against a PO that has cleared approval.
- **Segregation of duties.** The user who created a requisition/PO cannot approve it (HTTP 403).
- **Budget lifecycle (three-stage accrual).** `remaining = allocated − pre_committed − committed − spent`. Approving a requisition **pre-encumbers** its total (rejected if it would exceed the budget); raising a PO converts that estimate into a firm **encumbrance** (`committed`, also released on PO reject/cancel/delete); posting a GRN recognizes actual **spend** proportionally to the received line value (`committed → spent`), reversible on un-post/delete. See [Period close](#period-close) for open-encumbrance handling.
- **Three-way match.** New invoices get a `matchStatus` of `No PO` / `Pending GRN` / `Matched` / `Amount variance` from the linked PO and goods receipt.

### Period close

Open encumbrances are resolved at period end via `POST /api/v1/admin/budgets/close-period` (perm `procurement.manage_budget_period`) with `{policy: "lapse"|"carry", period?, budgetId?}` — `lapse` releases `pre_committed`+`committed` back to `remaining`, `carry` retains them. The same runs automatically as a daily job when `PROCUREMENT_PERIOD_CLOSE_ENABLED=true` (closing budgets past `period_end`).

### RFQ → quote → award

| Method | Path | Description |
|--------|------|-------------|
| GET | `/rfqs/:id/quotes` | List buyer-recorded vendor quotes |
| POST | `/rfqs/:id/quotes` | Record a vendor quote `{vendorId, amount, currency, notes}` |
| POST | `/rfqs/:id/award` | Award `{quoteId\|vendorId, budgetId?, expectedDate?}` → marks the RFQ `Awarded` and creates a draft PO from the winning quote |

### List pagination (opt-in, backward compatible)

`/vendors`, `/items`, `/requisitions`, `/purchase-orders`, and `/invoices` return the full array by default; pass any of `?limit` (≤500), `?offset`, or `?q` to page/filter from the DB instead.

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

- [Frontend integration (Next.js)](docs/FRONTEND_INTEGRATION.md) — [docs/frontend.env.example](docs/frontend.env.example)
- [Platform integration](docs/PLATFORM_INTEGRATION.md)

Registry: [`subrepos.json`](../../../subrepos.json)
