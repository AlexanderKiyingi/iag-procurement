-- Three-stage budget accrual: pre_committed -> committed -> spent.
-- Pre-encumbrance is reserved at requisition approval, converted to a firm
-- encumbrance when a PO is raised, and recognized as actual expenditure
-- proportionally as goods are received. Adds period-close bookkeeping.
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

-- Budget: soft reservation stage + period-close lifecycle.
-- remaining is now allocated - pre_committed - committed - spent.
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS pre_committed NUMERIC NOT NULL DEFAULT 0;

ALTER TABLE budgets ADD COLUMN IF NOT EXISTS period_end DATE;

ALTER TABLE budgets ADD COLUMN IF NOT EXISTS period_closed_at TIMESTAMPTZ;

-- Purchase order: firm-encumbrance idempotency flag + the running amount already
-- recognized as spend (supersedes the boolean budget_spent).
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS budget_committed BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS spent_recognized NUMERIC NOT NULL DEFAULT 0;

-- Requisition: tracks whether its pre-encumbrance has been liquidated by a PO,
-- so sibling POs don't release it twice. (requisitions.budget_committed now
-- means "pre-encumbered".)
ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS pre_released BOOLEAN NOT NULL DEFAULT FALSE;

-- GRN: idempotency flag + the exact amount recognized, so an un-post / delete can
-- reverse a proportional receipt.
ALTER TABLE grns ADD COLUMN IF NOT EXISTS budget_recognized BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE grns ADD COLUMN IF NOT EXISTS recognized_amount NUMERIC NOT NULL DEFAULT 0;

-- Goods-receipt lines: per-receipt value drives proportional spend recognition.
CREATE TABLE IF NOT EXISTS grn_lines (
    id SERIAL PRIMARY KEY,
    grn_id TEXT NOT NULL REFERENCES grns (id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES items (id),
    qty NUMERIC NOT NULL,
    unit_price NUMERIC NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_grn_lines_grn ON grn_lines (grn_id);

-- Backfill: legacy fully-received POs (old boolean budget_spent) already had
-- their full total moved to spent, so seed spent_recognized to match and avoid
-- re-recognition under the new proportional path.
UPDATE purchase_orders SET spent_recognized = total WHERE budget_spent = TRUE;
