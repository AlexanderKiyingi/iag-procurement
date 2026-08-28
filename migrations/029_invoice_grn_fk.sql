-- An invoice must not lose the goods receipt it was matched against, which is
-- the evidence the goods actually arrived. Verified zero orphans across 533
-- invoices before constraining.
ALTER TABLE procurement.invoices
  DROP CONSTRAINT IF EXISTS invoices_grn_id_fkey;
ALTER TABLE procurement.invoices
  ADD CONSTRAINT invoices_grn_id_fkey FOREIGN KEY (grn_id)
  REFERENCES procurement.grns (id) ON DELETE RESTRICT;

-- The monolith records a recurring template's end as free text: one reads
-- "Until further notice" where a date was expected. That means open-ended,
-- which ends_on NULL already expresses, but the wording is what someone wrote.
ALTER TABLE procurement.recurring_invoices
  ADD COLUMN IF NOT EXISTS ends_on_text TEXT NOT NULL DEFAULT '';
