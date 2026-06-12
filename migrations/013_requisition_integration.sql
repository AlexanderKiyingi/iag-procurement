-- Inter-service requisition hardening: durable outbound events, consumer
-- idempotency, lossless PM import, budget encumbrance, and RFQ/PO traceability.
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

-- Transactional outbox for procurement -> iag.commercial events. Approval/
-- rejection outcomes are enqueued in the same tx as the status change and
-- drained to Kafka by a background publisher, so a broker outage delays
-- delivery instead of dropping it (previously a direct WriteMessages that only
-- logged on failure).
CREATE TABLE IF NOT EXISTS procurement_event_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    event_key TEXT,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    dispatched_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_procurement_outbox_pending ON procurement_event_outbox (available_at) WHERE dispatched_at IS NULL;

-- Consumer idempotency: ids of inbound commercial events already handled, so a
-- redelivery (rebalance / no-DLQ retry) is a no-op rather than reprocessing.
CREATE TABLE IF NOT EXISTS processed_events (
    event_id TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Preserve the requester's vendor hint (payee) and justification from the PM
-- requisition — both were previously dropped on import.
ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS payee TEXT NOT NULL DEFAULT '';

ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS justification TEXT NOT NULL DEFAULT '';

-- Idempotency flag for budget encumbrance: an approved requisition commits its
-- total against the budget exactly once; rejection of a committed requisition
-- releases it.
ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS budget_committed BOOLEAN NOT NULL DEFAULT FALSE;

-- Traceability: which requisition drove an RFQ / PO.
ALTER TABLE rfqs ADD COLUMN IF NOT EXISTS requisition_id TEXT REFERENCES requisitions (id);

ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS requisition_id TEXT REFERENCES requisitions (id);
