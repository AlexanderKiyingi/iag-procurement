-- Trigram indexes for the list-page search boxes.
--
-- Every list endpoint searches with a LEADING-wildcard ILIKE across three to
-- five columns at once, e.g.
--
--   WHERE id ILIKE '%q%' OR title ILIKE '%q%'
--      OR vendor_id ILIKE '%q%' OR status ILIKE '%q%'
--
-- A B-tree cannot serve a leading wildcard, so each of those is a sequential
-- scan and the OR makes it several — on every keystroke of a vendor or PO
-- search. pg_trgm's GIN indexes are built for exactly this shape: they index
-- three-character substrings, so `%q%` becomes an index lookup.
--
-- gin_trgm_ops rather than gist_trgm_ops: GIN is slower to build and update but
-- substantially faster to search, and these are read-heavy reference tables
-- where a search is far more common than a write.
--
-- DEPLOY NOTE: the migration runner wraps every migration in one transaction,
-- and CREATE INDEX CONCURRENTLY cannot run inside one. So these take an ACCESS
-- EXCLUSIVE lock and block writes to each table while they build. On the
-- current row counts that is seconds; if these tables have grown by the time
-- this runs somewhere large, build them by hand with CONCURRENTLY first — the
-- IF NOT EXISTS clauses then make this migration a no-op.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Vendors: name, category, country, email
CREATE INDEX IF NOT EXISTS vendors_name_trgm_idx     ON vendors     USING GIN (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS vendors_category_trgm_idx ON vendors     USING GIN (category gin_trgm_ops);
CREATE INDEX IF NOT EXISTS vendors_country_trgm_idx  ON vendors     USING GIN (country gin_trgm_ops);
CREATE INDEX IF NOT EXISTS vendors_email_trgm_idx    ON vendors     USING GIN (email gin_trgm_ops);

-- Items: sku, name, category
CREATE INDEX IF NOT EXISTS items_sku_trgm_idx        ON items       USING GIN (sku gin_trgm_ops);
CREATE INDEX IF NOT EXISTS items_name_trgm_idx       ON items       USING GIN (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS items_category_trgm_idx   ON items       USING GIN (category gin_trgm_ops);

-- Requisitions: title, dept, requester
-- `status` is deliberately left out here and below. It has a handful of
-- distinct values, so a trigram index on it would be large, churn on every
-- status change, and never beat a scan — the planner would ignore it anyway.
CREATE INDEX IF NOT EXISTS requisitions_title_trgm_idx     ON requisitions USING GIN (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS requisitions_dept_trgm_idx      ON requisitions USING GIN (dept gin_trgm_ops);
CREATE INDEX IF NOT EXISTS requisitions_requester_trgm_idx ON requisitions USING GIN (requester gin_trgm_ops);

-- Purchase orders: id, title, vendor_id
CREATE INDEX IF NOT EXISTS purchase_orders_id_trgm_idx        ON purchase_orders USING GIN (id gin_trgm_ops);
CREATE INDEX IF NOT EXISTS purchase_orders_title_trgm_idx     ON purchase_orders USING GIN (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS purchase_orders_vendor_id_trgm_idx ON purchase_orders USING GIN (vendor_id gin_trgm_ops);

-- Invoices: invoice_no, id, vendor_id
-- COALESCE(invoice_no,'') in the query would not match a plain-column index, so
-- this indexes the same expression the predicate uses.
CREATE INDEX IF NOT EXISTS invoices_invoice_no_trgm_idx ON invoices USING GIN ((COALESCE(invoice_no, '')) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS invoices_id_trgm_idx         ON invoices USING GIN (id gin_trgm_ops);
CREATE INDEX IF NOT EXISTS invoices_vendor_id_trgm_idx  ON invoices USING GIN (vendor_id gin_trgm_ops);
