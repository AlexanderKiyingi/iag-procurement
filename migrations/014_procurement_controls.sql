-- Procurement controls: PO approval workflow + segregation of duties, goods-
-- receipt budget recognition, and RFQ quote capture / award.
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

-- Who created a PO, so approval can be refused for the creator (segregation of
-- duties). Defaults to '' for legacy rows.
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';

-- Idempotency flag for goods-receipt spend recognition: when the first GRN for
-- a PO is posted, the PO total moves committed -> spent on its budget exactly
-- once, regardless of how many partial receipts follow.
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS budget_spent BOOLEAN NOT NULL DEFAULT FALSE;

-- When an RFQ is awarded.
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS awarded_at TIMESTAMPTZ;

-- Buyer-recorded vendor quotes against an RFQ. Awarding a winning quote turns it
-- into a draft purchase order (traceable via rfqs.winner_vendor_id and the PO's
-- requisition_id).
CREATE TABLE IF NOT EXISTS rfq_quotes (
    id TEXT PRIMARY KEY,
    rfq_id TEXT NOT NULL REFERENCES rfqs (id) ON DELETE CASCADE,
    vendor_id TEXT NOT NULL REFERENCES vendors (id),
    amount NUMERIC NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_rfq_quotes_rfq ON rfq_quotes (rfq_id);
