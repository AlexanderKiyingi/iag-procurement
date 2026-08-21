-- 023: Mark which items are held as stock.
--
-- A goods receipt against a purchase order is the single accounting event for
-- that delivery (see iag-finance internal/ledger/inventory.go,
-- SourceDocProcurementGRN). Finance therefore has to know how much of the
-- received value capitalises into inventory and how much is period expense —
-- and only procurement knows which lines are stockable, because it owns the item
-- master.
--
-- Default TRUE: a purchase order line that gets a goods-receipt note is
-- physical goods far more often than not, and capitalising is the conservative
-- default for a receipt that has been delivered but not yet invoiced. Mark
-- services, subscriptions and direct-to-site consumables FALSE so their receipts
-- expense instead.
ALTER TABLE items ADD COLUMN IF NOT EXISTS stockable BOOLEAN NOT NULL DEFAULT TRUE;

-- Items procured as services rather than goods never hold stock. This is a
-- best-effort pass over the existing catalogue by category; anything it misses
-- simply capitalises until someone marks it, which is visible and correctable
-- rather than silent.
UPDATE items
SET stockable = FALSE
WHERE stockable = TRUE
  AND lower(category) IN ('service', 'services', 'subscription', 'subscriptions', 'labour', 'labor');
