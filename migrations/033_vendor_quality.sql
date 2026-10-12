-- 033: supplier quality status
--
-- Procurement decides whether a supplier is approved, conditional or
-- suspended; quality (iag-quality-control 023) supplies the evidence as
-- qc.supplier.scorecard events. The two are kept apart on purpose:
--
--   quality_status, _reason, _by, _at   procurement's decision, set through
--                                       PATCH /vendors/:id/quality-status with
--                                       a reason; '' means not yet decided
--   qc_recommendation, qc_acceptance_rate, qc_reasons, qc_recommended_at
--                                       quality's latest advice, written only
--                                       by the consumer, never by a user
--
-- A suspended supplier cannot be given a new purchase order (enforced in
-- CreatePurchaseOrder / UpdatePurchaseOrder). Advice alone suspends nobody.

ALTER TABLE vendors
    ADD COLUMN IF NOT EXISTS quality_status        TEXT NOT NULL DEFAULT ''
        CHECK (quality_status IN ('', 'approved', 'conditional', 'suspended')),
    ADD COLUMN IF NOT EXISTS quality_status_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality_status_by     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality_status_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS qc_recommendation     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS qc_acceptance_rate    DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS qc_reasons            JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS qc_recommended_at     TIMESTAMPTZ;
