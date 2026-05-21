-- PM workspace requisition imports (idempotent consumer on iag.commercial).

ALTER TABLE requisitions
    ADD COLUMN IF NOT EXISTS pm_requisition_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS requisitions_pm_requisition_id_key
    ON requisitions (pm_requisition_id)
    WHERE pm_requisition_id IS NOT NULL;
