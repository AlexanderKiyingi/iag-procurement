-- Add a Django-admin-like staff gate for admin endpoints.
-- Superusers are implicitly staff, but the flag is separate so you can have non-superuser staff accounts.

ALTER TABLE auth_users
    ADD COLUMN IF NOT EXISTS is_staff BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: existing superusers become staff.
UPDATE auth_users
SET is_staff = TRUE
WHERE is_superuser = TRUE AND is_staff = FALSE;

