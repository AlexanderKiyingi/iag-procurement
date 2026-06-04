-- Phase 4: SCM party linkage for vendor sync via Kafka

ALTER TABLE vendors
    ADD COLUMN IF NOT EXISTS scm_business_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_vendors_scm_business_id
    ON vendors (scm_business_id) WHERE scm_business_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS kafka_dedupe (
    event_id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
