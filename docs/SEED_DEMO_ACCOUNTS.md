# Bootstrap users (empty database)

When the API starts, `rbac.Seed` runs after migrations. If `auth_users` has **no rows**, it creates permissions, groups, and one user for local or first-time setup.

**Source:** `internal/rbac/seed.go`

## When seeding runs

- Condition: `COUNT(*)` from `auth_users` is zero **inside** the seed transaction (after a DB lock; see below).
- If you already have users, seed does nothing (no overwrite).

## Deployment: run once, safe under multiple replicas

Two layers apply:

1. **SQL migrations** (`internal/migrate`) — including procurement seed data in `002_data.sql`. Each version is recorded in `schema_migrations` and skipped on later starts. `migrate.Up` runs inside **one transaction** with `pg_advisory_xact_lock`, so many API pods starting together **cannot** double-apply the same migration batch; they serialize on that lock.

2. **RBAC bootstrap** (`rbac.Seed`) — runs after migrations, also under **`pg_advisory_xact_lock`** (different key pair). Only one process inserts bootstrap users; others wait, then see `auth_users` non-empty and exit without inserting.

So seed data is applied **once per environment**: migrations once per version, RBAC defaults once per empty `auth_users`. Routine pod restarts do not re-seed.

## Environment

| Variable | Effect |
|----------|--------|
| `DEFAULT_ADMIN_PASSWORD` | If set (non-empty), used as the plaintext password for **`admin@iag.local`** before hashing. If unset, the default **`admin123`** is used. |

Change all passwords in production after first login.

## Accounts created

| Email | `is_superuser` | Group | Password (initial) |
|-------|----------------|-------|---------------------|
| `admin@iag.local` | `true` | Administrators | `DEFAULT_ADMIN_PASSWORD` or **`admin123`** |

> **`viewer@iag.local` is no longer created.** It was a demo login with the hard-coded
> password `viewer123`, so it went with the rest of the platform's seed data; migration
> `025_purge_demo_seed.sql` removes it from databases that already have it. The
> **Viewers** group and its read permissions remain — they are authorisation
> configuration. Assign a real user to that group instead.

## Groups and permissions (summary)

- **Administrators** — all bootstrap permissions (full app + RBAC admin where granted by migration).
- **Viewers** — read-oriented set: view seed data, API audit log, and notifications inbox (`view_seed`, `view_api_audit`, `view_inbox`).

Exact permission codes are defined in `internal/rbac/seed.go` (`bootstrapPermissions` and `viewOnly`).

## Logs

On successful bootstrap the API logs:

```text
rbac: bootstrapped admin@iag.local (dev password — change in production)
```

## Production checklist

1. Set a strong `DEFAULT_ADMIN_PASSWORD` before the **first** start against an empty database, or change `admin@iag.local` immediately after login.
2. Confirm `viewer@iag.local` is absent — migration `025_purge_demo_seed.sql` removes it, and the bootstrap no longer creates it.
3. Prefer creating real users via your admin workflow once RBAC is configured.
