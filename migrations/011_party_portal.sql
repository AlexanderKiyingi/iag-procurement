-- Phase 4: party linkage for vendor portal

ALTER TABLE vendors
    ADD COLUMN IF NOT EXISTS party_id UUID,
    ADD COLUMN IF NOT EXISTS platform_user_id UUID;

CREATE INDEX IF NOT EXISTS idx_vendors_party_id ON vendors (party_id);
CREATE INDEX IF NOT EXISTS idx_vendors_platform_user ON vendors (platform_user_id);
