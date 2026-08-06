-- Desk-based requisition approval, alongside the amount-band tiers of 018.
--
-- 018 answers "how many signatures does this amount need". This answers "whose
-- desk is it on right now, and what happened at each desk before". The two
-- compose: a chain lists its desks in order, and a desk carries a min_amount so
-- a small requisition never reaches GM or CEO while a large one does. Bands
-- decide WHETHER a desk engages; the chain decides WHEN.
--
-- Nothing here replaces requisition_approval_tiers or requisition_approvals —
-- the tiered endpoints keep working untouched. A requisition uses one mechanism
-- or the other: it has a row here, or it does not.
--
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

CREATE TABLE IF NOT EXISTS requisition_approval_state (
    requisition_id TEXT PRIMARY KEY REFERENCES requisitions (id) ON DELETE CASCADE,
    chain_key TEXT NOT NULL,
    status TEXT NOT NULL,
    desk TEXT NOT NULL DEFAULT '',
    stage_index INTEGER NOT NULL DEFAULT -1,
    amount NUMERIC NOT NULL DEFAULT 0,
    skip TEXT[] NOT NULL DEFAULT '{}',
    requester TEXT NOT NULL DEFAULT '',
    -- Who the request belongs to, keyed by the names desks scope on
    -- (project_owner, department). Captured at open: ownership at submission is
    -- what the approval refers to.
    scope JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The desk queue query is "open requests whose current desk is one of mine", so
-- it is served entirely by this index rather than a scan over every requisition.
CREATE INDEX IF NOT EXISTS requisition_approval_state_desk_idx
    ON requisition_approval_state (desk, status)
    WHERE status = 'in_flight';

CREATE INDEX IF NOT EXISTS requisition_approval_state_requester_idx
    ON requisition_approval_state (requester, status);

-- One row per transition: the audit trail. seq makes history totally ordered
-- even when two steps land in the same millisecond, which decided_at alone
-- cannot guarantee.
CREATE TABLE IF NOT EXISTS requisition_approval_steps (
    id BIGSERIAL PRIMARY KEY,
    requisition_id TEXT NOT NULL REFERENCES requisitions (id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    desk TEXT NOT NULL DEFAULT '',
    desk_label TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    actor_role TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    override BOOLEAN NOT NULL DEFAULT FALSE,
    acted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_requisition_approval_steps_seq
    ON requisition_approval_steps (requisition_id, seq);

CREATE INDEX IF NOT EXISTS requisition_approval_steps_req_idx
    ON requisition_approval_steps (requisition_id, seq);

-- A rejection or an amendment must say why. The engine enforces this too, but
-- the constraint means no code path — a script, a backfill, a future handler —
-- can write a silent refusal into the audit trail.
ALTER TABLE requisition_approval_steps
    DROP CONSTRAINT IF EXISTS requisition_approval_steps_reason_required;

ALTER TABLE requisition_approval_steps
    ADD CONSTRAINT requisition_approval_steps_reason_required
    CHECK (action NOT IN ('reject', 'amend') OR length(btrim(reason)) > 0);

-- The desk matrix: which roles hold each desk, and from what amount it engages.
-- Editable in place, like requisition_approval_tiers — role names are deployment
-- data, so role_patterns holds case-insensitive regexes rather than exact names
-- ("GM" and "General Manager" are the same desk).
CREATE TABLE IF NOT EXISTS requisition_approval_desks (
    chain_key TEXT NOT NULL,
    position INTEGER NOT NULL,
    desk TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    role_patterns TEXT[] NOT NULL DEFAULT '{}',
    min_amount NUMERIC NOT NULL DEFAULT 0,
    required_perm TEXT NOT NULL DEFAULT '',
    action_label TEXT NOT NULL DEFAULT '',
    status_label TEXT NOT NULL DEFAULT '',
    -- Narrows the desk from "anyone with the role" to "the owner of this
    -- request": names a key in requisition_approval_state.scope. Empty leaves
    -- the desk role-global.
    scope_by TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (chain_key, desk)
);

CREATE INDEX IF NOT EXISTS requisition_approval_desks_order_idx
    ON requisition_approval_desks (chain_key, position);

-- Default matrix. Bands mirror 018 so the two mechanisms escalate at the same
-- amounts: supervisor work below 5M, manager to 20M, director above.
-- The PM desk is scoped to the request's own project manager: any PM holding
-- the role could clear any request otherwise, which is not what "the Project
-- Manager approves it" means. The senior desks stay role-wide — a GM approves
-- on behalf of the company, not a project.
INSERT INTO requisition_approval_desks
    (chain_key, position, desk, label, role_patterns, min_amount, required_perm, action_label, status_label, scope_by)
VALUES
    ('requisition', 1, 'pm', 'Project Manager',
     ARRAY['project\s*manager', '\bpm\b'], 0, 'procurement.change_requisition', 'Approve', 'PM Approved', 'project_owner'),
    ('requisition', 2, 'accounts', 'Accounts Assistant',
     ARRAY['accounts?\s*assistant', 'accounts?\s*asst', '\baccountant\b', '\baa\b'], 0,
     'procurement.approve_requisition_tier1', 'Approve', 'Accounts Assistant Approved', ''),
    ('requisition', 3, 'gm', 'General Manager',
     ARRAY['general\s*manager', '\bgm\b', 'gen\.?\s*manager'], 5000000,
     'procurement.approve_requisition_tier2', 'Approve', 'GM Approved', ''),
    ('requisition', 4, 'ceo', 'CEO',
     ARRAY['\bceo\b', 'chief\s*executive'], 20000000,
     'procurement.approve_requisition_tier3', 'Approve', 'CEO Approved', ''),
    ('requisition', 5, 'finance', 'Finance',
     ARRAY['\bfinance\b', 'finance\s*(manager|officer|clerk)', 'cashier', 'treasurer'], 0,
     'procurement.approve_requisition_tier1', 'Make payment', 'Paid', '')
ON CONFLICT (chain_key, desk) DO NOTHING;

-- The material request chain forks at submit on stock availability. Stores path:
-- the storekeeper issues from stock and no money moves, so it ends at Issued.
INSERT INTO requisition_approval_desks
    (chain_key, position, desk, label, role_patterns, min_amount, required_perm, action_label, status_label, scope_by)
VALUES
    ('material.stores', 1, 'qs', 'Quantity Surveyor',
     ARRAY['quantity\s*surveyor', '\bqs\b'], 0, 'procurement.change_requisition', 'Approve', 'QS Approved', ''),
    ('material.stores', 2, 'pm', 'Project Manager',
     ARRAY['project\s*manager', '\bpm\b'], 0, 'procurement.change_requisition', 'Approve', 'PM Approved', 'project_owner'),
    ('material.stores', 3, 'stores', 'Stores',
     ARRAY['stores?\s*(manager|keeper|officer)?', 'storekeeper', 'warehouse\s*manager'], 0,
     'procurement.change_requisition', 'Issue materials', 'Issued', '')
ON CONFLICT (chain_key, desk) DO NOTHING;

-- Procurement path: stock is short, so the request becomes a purchase and walks
-- the money chain, ending with procurement closing the loop after payment.
INSERT INTO requisition_approval_desks
    (chain_key, position, desk, label, role_patterns, min_amount, required_perm, action_label, status_label, scope_by)
VALUES
    ('material.procurement', 1, 'finance_review', 'Finance review',
     ARRAY['\bfinance\b', 'finance\s*(manager|officer)', '\baccountant\b'], 0,
     'procurement.approve_requisition_tier1', 'Approve', 'Finance Reviewed', ''),
    ('material.procurement', 2, 'gm', 'General Manager',
     ARRAY['general\s*manager', '\bgm\b', 'gen\.?\s*manager'], 5000000,
     'procurement.approve_requisition_tier2', 'Approve', 'GM Approved', ''),
    ('material.procurement', 3, 'ceo', 'CEO',
     ARRAY['\bceo\b', 'chief\s*executive'], 20000000,
     'procurement.approve_requisition_tier3', 'Approve', 'CEO Approved', ''),
    ('material.procurement', 4, 'finance_pay', 'Finance payment',
     ARRAY['\bfinance\b', 'cashier', 'treasurer'], 0,
     'procurement.approve_requisition_tier1', 'Make payment', 'Paid', ''),
    ('material.procurement', 5, 'procurement_followup', 'Procurement follow-up',
     ARRAY['procurement', 'purchasing', 'buyer'], 0,
     'procurement.change_requisition', 'Complete follow-up', 'Follow-up Complete', '')
ON CONFLICT (chain_key, desk) DO NOTHING;
