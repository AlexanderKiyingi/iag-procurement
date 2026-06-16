-- Tiered, amount-based requisition approval.
--
-- requisition_approval_tiers is the editable approval matrix: a requisition of
-- total T requires sign-off from every tier whose min_amount < T, cleared
-- low-to-high by DISTINCT approvers each holding that tier's required_perm
-- (so a 25M requisition needs tier1 + tier2 + tier3 signatures). Edit the rows
-- to change bands or which permission gates each tier.
--
-- requisition_approvals is the per-tier decision ledger — the audit trail plus
-- the idempotency guard (a tier can be approved at most once per requisition).
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

CREATE TABLE IF NOT EXISTS requisition_approval_tiers (
    tier INTEGER PRIMARY KEY,
    label TEXT NOT NULL DEFAULT '',
    min_amount NUMERIC NOT NULL DEFAULT 0,
    max_amount NUMERIC,
    required_perm TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requisition_approvals (
    id TEXT PRIMARY KEY,
    requisition_id TEXT NOT NULL REFERENCES requisitions (id) ON DELETE CASCADE,
    tier INTEGER NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS requisition_approvals_req_idx ON requisition_approvals (requisition_id);

-- A tier may be approved at most once per requisition (idempotency / no double-sign).
-- Rejections are unconstrained so a re-rejection is still recorded.
CREATE UNIQUE INDEX IF NOT EXISTS uq_requisition_approvals_tier ON requisition_approvals (requisition_id, tier) WHERE decision = 'approved';

-- Default approval matrix (UGX bands). Editable in place — update min/max or the
-- required_perm to re-route a tier; ON CONFLICT keeps existing edits on re-run.
INSERT INTO requisition_approval_tiers (tier, label, min_amount, max_amount, required_perm)
VALUES
    (1, 'Supervisor', 0,        5000000,  'procurement.approve_requisition_tier1'),
    (2, 'Manager',    5000000,  20000000, 'procurement.approve_requisition_tier2'),
    (3, 'Director',   20000000, NULL,     'procurement.approve_requisition_tier3')
ON CONFLICT (tier) DO NOTHING;
