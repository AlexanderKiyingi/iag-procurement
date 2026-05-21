-- Django-style RBAC: users, groups, permissions, M2M join tables

CREATE TABLE IF NOT EXISTS auth_permissions (
    id BIGSERIAL PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS auth_groups (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS auth_group_permissions (
    group_id BIGINT NOT NULL REFERENCES auth_groups (id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES auth_permissions (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, permission_id)
);

CREATE TABLE IF NOT EXISTS auth_users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_superuser BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS auth_user_groups (
    user_id BIGINT NOT NULL REFERENCES auth_users (id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES auth_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);

CREATE TABLE IF NOT EXISTS auth_user_permissions (
    user_id BIGINT NOT NULL REFERENCES auth_users (id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES auth_permissions (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, permission_id)
);

CREATE INDEX IF NOT EXISTS idx_auth_user_groups_user ON auth_user_groups (user_id);
CREATE INDEX IF NOT EXISTS idx_auth_group_permissions_group ON auth_group_permissions (group_id);

CREATE TABLE IF NOT EXISTS api_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id BIGINT REFERENCES auth_users (id),
    actor_email TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_api_audit_logs_created ON api_audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_audit_logs_user ON api_audit_logs (user_id);
