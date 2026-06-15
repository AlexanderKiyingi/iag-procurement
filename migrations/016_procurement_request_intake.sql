-- Generic inbound procurement requests: any service (stores/warehouse, fleet,
-- etc.) can emit `procurement.requested`; procurement records the originating
-- system and its reference so the same logical request is imported exactly once.
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS origin_system TEXT NOT NULL DEFAULT '';

ALTER TABLE requisitions ADD COLUMN IF NOT EXISTS origin_ref TEXT NOT NULL DEFAULT '';

-- One requisition per (origin_system, origin_ref) when a reference is present,
-- so a redelivered or duplicate request is a no-op rather than a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS uq_requisitions_origin ON requisitions (origin_system, origin_ref) WHERE origin_ref <> '';
