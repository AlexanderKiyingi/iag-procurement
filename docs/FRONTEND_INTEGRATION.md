# Procurement Frontend Integration Guide

Comprehensive guide for connecting a **Next.js** app to the procurement
backend. Covers auth, gateway paths, the permission model, seed snapshot,
vendor portal, and route catalog.

For deployment-side env config see [PLATFORM_INTEGRATION.md](./PLATFORM_INTEGRATION.md)
(note: gateway-secret / `X-IAG-*` header auth described there is **removed** —
production uses Bearer JWT only). For legacy standalone auth see
[SEED_DEMO_ACCOUNTS.md](./SEED_DEMO_ACCOUNTS.md).

---

## 1. Authentication

Procurement runs in **platform Bearer+aud mode** (`AUTH_MODE=jwt`, default).
Every request — except health probes — requires:

```
Authorization: Bearer <jwt>
```

The JWT must carry `aud=iag.procurement`. The service verifies signatures
locally against the auth service's JWKS.

### Two-hop audience (gateway + procurement)

1. **Gateway** verifies `aud=iag.gateway`.
2. Gateway forwards `Authorization` verbatim.
3. **Procurement** re-verifies with `aud=iag.procurement`.

### Token acquisition

```
POST /api/v1/authentication/oauth/token
  grant_type=password | refresh_token
→ access_token, refresh_token
```

**Frontend responsibilities:**
- Keep `access_token` in memory; refresh ~1 minute before expiry.
- On 401, attempt refresh; on second 401, redirect to login.
- On 403, hide the UI control — backend re-checks on every mutation.

### Common 401 / 403 causes

1. Token expired.
2. `aud` missing `iag.procurement` or `iag.gateway`.
3. Missing **`platform.access_procurement`** at gateway.
4. Missing `procurement.view_seed` (reads) or mutation codename (writes).
5. `AUTH_MODE=legacy` only applies to standalone dev — do not use in Next.js
   against the platform gateway.

---

## 2. Base URLs

| Environment | API base |
|---|---|
| Local direct | `http://localhost:4009/api/v1` |
| Local via gateway | `http://localhost:8080/api/v1/procurement/api/v1` |
| Production | `https://iag-api-gateway-production.up.railway.app/api/v1/procurement/api/v1` |

**Path quirk:** the gateway URL contains **double** `/api/v1` — this is
intentional. Example:

```
GET /api/v1/procurement/api/v1/requisitions
```

**Always go through the gateway in non-local environments.**

### Required frontend env vars

Copy [frontend.env.example](./frontend.env.example) to your Next.js app as
`.env.local` (local) or platform secrets (production).

```env
# Local (via gateway)
NEXT_PUBLIC_PROCUREMENT_API_URL=http://localhost:8080/api/v1/procurement/api/v1
NEXT_PUBLIC_AUTH_API_URL=http://localhost:8080/api/v1/authentication
NEXT_PUBLIC_GATEWAY_ORIGIN=http://localhost:8080
```

```env
# Production (Railway, via gateway)
NEXT_PUBLIC_PROCUREMENT_API_URL=https://iag-api-gateway-production.up.railway.app/api/v1/procurement/api/v1
NEXT_PUBLIC_AUTH_API_URL=https://iag-api-gateway-production.up.railway.app/api/v1/authentication
NEXT_PUBLIC_GATEWAY_ORIGIN=https://iag-api-gateway-production.up.railway.app
```

### CORS

Set `CORS_ALLOW_ORIGIN` (or legacy aliases) to include your Next.js origin.
Auth is via `Authorization` header — no cookies.

---

## 3. Permission Model

Procurement uses **Django-style `procurement.*` codenames**.

### 3.1 Core codenames

| Codename | Use |
|---|---|
| `procurement.view_seed` | Read aggregate snapshot and all list endpoints |
| `procurement.add_*` / `change_*` / `delete_*` | Entity mutations (requisition, PO, vendor, …) |
| `procurement.view_own_po` | Vendor portal purchase orders |
| `procurement.view_own_invoice` | Vendor portal invoices |
| `procurement.view_inbox` | In-app notifications list |
| `procurement.emit_notification` | Emit notification events |
| `audit.view_api_log` | Admin audit log |

**Catalog source:** [internal/rbac/codes.go](../internal/rbac/codes.go),
[internal/models/permissions.go](../internal/models/permissions.go)

### 3.2 Gateway service gate

Every proxied route also requires **`platform.access_procurement`**.

Gateway coarse sets (any one passes):
- **View (GET):** `procurementViewPermissions` in
  `shared/services/api-gateway/src/service-permissions.ts`
- **Mutate (POST/PATCH/DELETE):** `procurementMutatePermissions`

**Portal routes** (`/portal/*`): gateway requires authentication only; service
enforces `procurement.view_own_po` / `procurement.view_own_invoice`.

Superusers bypass all permission checks.

---

## 4. App boot sequence (Next.js)

Procurement has no `/bootstrap` endpoint. Recommended flow after login:

| Step | Endpoint | Purpose |
|---|---|---|
| 1 | `GET /auth/me` | `{ userId, email, isSuperuser, permissions }` |
| 2 | `GET /seed` | Full cached snapshot (vendors, items, requisitions, POs, GRNs, invoices, …) |
| 3 | Individual list routes | Incremental refresh after mutations |

`GET /seed` requires `procurement.view_seed` and returns the full workspace
document the SPA typically mounts in one round-trip.

---

## 5. Endpoint Catalog

All routes prefixed with base URL (§2). Gateway also enforces
`platform.access_procurement`.

### 5.1 Public probes (no auth)

| Method | Path | Description |
|---|---|---|
| GET | `/health`, `/healthz` | Liveness |
| GET | `/ready` | Readiness |

Gateway: `/api/v1/procurement/health`, `/ready`, `/healthz`.

### 5.2 Session

| Method | Path | Permission |
|---|---|---|
| GET | `/auth/me` | Authenticated |

### 5.3 Read snapshot & lists (`procurement.view_seed`)

| Method | Path | Description |
|---|---|---|
| GET | `/seed` | Full snapshot |
| GET | `/vendors` | Vendor list |
| GET | `/items` | Item catalogue |
| GET | `/budgets` | Budget lines |
| GET | `/requisitions` | Requisitions |
| GET | `/rfqs` | RFQs |
| GET | `/purchase-orders` | Purchase orders |
| GET | `/orders` | Orders alias |
| GET | `/grns` | Goods receipt notes |
| GET | `/invoices` | Invoices |
| GET | `/contracts` | Contracts |
| GET | `/payments` | Payments |
| GET | `/audit` | Domain audit trail |

### 5.4 Mutations

| Resource | POST | PATCH | DELETE |
|---|---|---|---|
| Requisitions | `/requisitions` | `/requisitions/:id` | `/requisitions/:id` |
| Purchase orders | `/purchase-orders` | `/purchase-orders/:id` | `/purchase-orders/:id` |
| Vendors | `/vendors` | `/vendors/:id` | `/vendors/:id` |
| Items | `/items` | `/items/:id` | `/items/:id` |
| Budgets | `/budgets` | `/budgets/:id` | `/budgets/:id` |
| RFQs | `/rfqs` | `/rfqs/:id` | `/rfqs/:id` |
| GRNs | `/grns` | `/grns/:id` | `/grns/:id` |
| Invoices | `/invoices` | `/invoices/:id` | `/invoices/:id` |
| Contracts | `/contracts` | `/contracts/:id` | `/contracts/:id` |

Each mutation requires the matching `procurement.add_*`, `procurement.change_*`,
or `procurement.delete_*` codename.

### 5.5 Notifications

| Method | Path | Permission |
|---|---|---|
| GET | `/notifications` | `procurement.view_inbox` |
| PATCH | `/notifications/:id/read` | `procurement.view_inbox` |
| POST | `/notifications/emit` | `procurement.emit_notification` |

### 5.6 Vendor portal (row-scoped)

| Method | Path | Permission |
|---|---|---|
| GET | `/portal/me` | `procurement.view_own_po` or `procurement.view_own_invoice` |
| GET | `/portal/purchase-orders` | `procurement.view_own_po` |
| GET | `/portal/invoices` | `procurement.view_own_invoice` |

Portal data is scoped by `vendors.platform_user_id` linked to the JWT `sub`.

Cross-service supplier portal also uses:
- SCM: `/api/v1/supply-chain/api/v1/portal/*`
- Finance: `/api/v1/finance/v1/portal/ap`

### 5.7 Admin

| Method | Path | Permission |
|---|---|---|
| GET | `/admin/audit-logs` | `audit.view_api_log` |

> `/api/v1/admin/users`, `/admin/groups`, etc. return **410 Gone** in jwt mode.
> Use iag-authentication admin APIs for IAM.

---

## 6. Error Conventions

| Status | Meaning | Frontend action |
|---|---|---|
| 400 | Validation | Inline field error |
| 401 | Missing / invalid token | Refresh → re-login |
| 403 | Permission denied | Hide control |
| 404 | Not found | Soft state / re-fetch |
| 409 | Conflict | Re-fetch and retry |
| 410 | Deprecated admin IAM route | Use auth admin API |
| 500 | Server error | Toast + retry |
| 503 | Upstream/DB down | Maintenance banner |

Response bodies typically follow `{"error":"message"}` or the platform
`apierr` envelope with `error.code`.

---

## 7. Next.js integration patterns

### Fetch helper

```ts
const base = process.env.NEXT_PUBLIC_PROCUREMENT_API_URL!;

export async function procurementFetch<T>(
  path: string,
  token: string,
  init?: RequestInit,
): Promise<T> {
  const res = await fetch(`${base}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...init?.headers,
    },
  });
  if (!res.ok) throw new Error(`Procurement ${res.status}: ${await res.text()}`);
  return res.json() as Promise<T>;
}
```

### Recommended boot in a Server Component or route handler

```ts
const me = await procurementFetch("/auth/me", token);
const seed = await procurementFetch("/seed", token);
// Pass seed to client components; refresh slices after mutations
```

### Cache invalidation

The seed endpoint is Redis-backed. After mutations, re-fetch `/seed` or the
specific list route (`/purchase-orders`, etc.) — there is no WebSocket on
this service.

---

## 8. Quickstart Checklist

- [ ] Set `NEXT_PUBLIC_PROCUREMENT_API_URL` and `NEXT_PUBLIC_AUTH_API_URL`.
- [ ] Implement OAuth login + silent refresh.
- [ ] Confirm JWT `aud` includes `iag.gateway` and `iag.procurement`.
- [ ] Confirm `platform.access_procurement` (or superadmin).
- [ ] On app load: `GET /auth/me` then `GET /seed`.
- [ ] Gate write UI on `procurement.add_*` / `change_*` codenames.
- [ ] Vendor portal: `/portal/me` → `/portal/purchase-orders`.
- [ ] Do not call embedded `/admin/users` — use auth admin API.

---

## See Also

- [README.md](../README.md)
- [DEPLOYMENT.md](./DEPLOYMENT.md)
- [docs/RBAC.md](../../../../docs/RBAC.md)
- SCM supplier portal: [MOBILE_APPS.md](../../IAG_SCM_backend/docs/MOBILE_APPS.md)
- Auth: [shared/services/authentication](../../../../shared/services/authentication)
