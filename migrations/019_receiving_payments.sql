-- 019_receiving_payments.sql
-- Closes three procure-to-pay gaps surfaced in the frontend↔backend audit:
--   1. PO receiving tracked quantities (not just recognized money), so a PO can
--      reach a "Received" state and per-line short/over receipts are visible.
--   2. Vendor payment state on the PO (fed by the finance.payment.made writeback
--      consumer) so the UI's paymentStatus stops being a static placeholder.
--   3. Invoice payment/link columns the frontend already reads (paymentDate,
--      paymentMethod, grnId) but the backend never persisted.

-- Per-line received quantity, incremented as GRNs are posted against the PO.
ALTER TABLE po_lines ADD COLUMN IF NOT EXISTS received_qty NUMERIC NOT NULL DEFAULT 0;

-- Vendor-payment lifecycle on the PO: 'Not Issued' → 'Issued' (a payment against
-- one of its invoices settled in finance). Kept as free text to match the rest of
-- the schema's string enums.
ALTER TABLE purchase_orders ADD COLUMN IF NOT EXISTS payment_status TEXT NOT NULL DEFAULT 'Not Issued';

-- Invoice settlement + linkage columns consumed by the web UI.
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS payment_date DATE;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS payment_method TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS grn_id TEXT;

-- Payments idempotency: a finance.payment.made settlement is keyed on the finance
-- payment ref; the writeback consumer must never double-insert on redelivery.
CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_reference ON payments (reference) WHERE reference <> '';
