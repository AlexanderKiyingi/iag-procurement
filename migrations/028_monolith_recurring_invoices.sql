-- Recurring purchase invoice templates from the ERP monolith
--
-- Created during the ERP monolith clone, when the tail of the migration turned
-- out to have no platform tables to land in. Recorded here so a fresh
-- environment reproduces them; IF NOT EXISTS makes this a no-op where they
-- already stand.
--
-- Cross-service references (project_id -> finance.projects) are plain columns
-- rather than foreign keys: the services deploy independently and a constraint
-- would couple their migration order. The accompanying *_name column carries
-- the source's own text, so the link survives even where the id does not.

-- next_issue_on is what a scheduler reads. Kept rather than derived so a paused
-- template does not silently resume.
CREATE TABLE IF NOT EXISTS procurement.recurring_invoices (
    id             UUID PRIMARY KEY,
    name           TEXT NOT NULL,
    party          TEXT NOT NULL DEFAULT '',
    account        TEXT NOT NULL DEFAULT '',
    description    TEXT NOT NULL DEFAULT '',
    amount         NUMERIC(18,4) NOT NULL DEFAULT 0,
    interval_spec  TEXT NOT NULL DEFAULT '',
    next_issue_on  DATE,
    starts_on      DATE,
    ends_on        DATE,
    status         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The source's "until" is free text: one template reads "Until further notice"
-- rather than a date. That phrase means open-ended, which ends_on NULL already
-- expresses - but the wording is what someone actually wrote, so it is kept
-- rather than silently discarded when it will not parse as a date.
ALTER TABLE procurement.recurring_invoices
  ADD COLUMN IF NOT EXISTS ends_on_text TEXT NOT NULL DEFAULT '';
