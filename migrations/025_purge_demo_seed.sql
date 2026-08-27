-- Purge the demo dataset seeded by 002_data.sql.
--
-- Scope: demo business records, plus the one demo login. Deliberately preserved:
--   * auth_groups / auth_permissions and their links (RBAC, 004_rbac.sql,
--     007_rbac_admin_write_grants.sql), and the admin@iag.local superuser.
--   * requisition_approval_tiers (018) — approval-band configuration, not demo data.
--   * items with id LIKE 'ITEM-FUEL-%' (017_fuel_catalogue.sql) — the fleet→procurement
--     fuel bridge resolves requisition lines against these SKUs. The demo catalogue
--     uses the 'ITM-%' prefix, so the predicates below cannot reach them.
--
-- Master-data rows (vendors, items, budgets) are deleted only when nothing still
-- references them, so a row an operator has since transacted against survives and the
-- migration cannot fail on a foreign key. Pure demo transactions are deleted outright.
--
-- 002_data.sql itself is left in place: it is already recorded in schema_migrations on
-- every existing database, and rewriting an applied migration would break the checksum
-- ledger. Its INSERTs are neutralised for fresh databases by running after it.

-- The migration runner already wraps every file in a single transaction holding an
-- advisory lock, so this file must not open one of its own: a COMMIT here would end
-- that outer transaction early and release the lock mid-run.

-- ---- transactions: payments → invoices ------------------------------------
DELETE FROM payments WHERE id LIKE 'PAY-2026-%';
DELETE FROM invoices WHERE id LIKE 'INV-V0%';

-- ---- receiving: grn_lines → grns ------------------------------------------
DELETE FROM grn_lines WHERE grn_id LIKE 'GRN-2026-%';
DELETE FROM grns      WHERE id     LIKE 'GRN-2026-%';

-- ---- sourcing: rfq_quotes → rfqs ------------------------------------------
DELETE FROM rfq_quotes WHERE rfq_id LIKE 'RFQ-2026-%';
DELETE FROM rfqs       WHERE id     LIKE 'RFQ-2026-%';

-- ---- ordering: po_lines → purchase_orders ---------------------------------
DELETE FROM po_lines        WHERE po_id LIKE 'PO-2026-%';
DELETE FROM purchase_orders WHERE id    LIKE 'PO-2026-%';

-- ---- contracts -------------------------------------------------------------
DELETE FROM contracts WHERE id LIKE 'CNT-202%';

-- ---- requisitions and their approval trail --------------------------------
-- The approval tables all cascade from requisitions, but delete them explicitly so
-- the intent is visible and the migration does not depend on the FK action.
DELETE FROM requisition_approvals      WHERE requisition_id LIKE 'PR-2026-%';
DELETE FROM requisition_approval_steps WHERE requisition_id LIKE 'PR-2026-%';
DELETE FROM requisition_approval_state WHERE requisition_id LIKE 'PR-2026-%';
DELETE FROM requisitions               WHERE id             LIKE 'PR-2026-%';

-- api_audit_logs and processed_events are keyed by resource_id / event_id rather than
-- a requisition FK; the seed wrote no rows into either, so neither is touched.

-- ---- seeded activity log ---------------------------------------------------
-- 002_data.sql inserted exactly five rows with explicit ids 1..5; live entries use
-- the sequence and start above them.
DELETE FROM audit_entries
WHERE id BETWEEN 1 AND 5
  AND target IN ('PR-2026-0050', 'PO-2026-0118', 'PAY-2026-0234', 'PR-2026-0049', 'GRN-2026-0089');

-- ---- master data (guarded: only when unreferenced) ------------------------
DELETE FROM budgets b
WHERE b.id LIKE 'BDG-2026-%'
  AND NOT EXISTS (SELECT 1 FROM requisitions     r WHERE r.budget_id = b.id)
  AND NOT EXISTS (SELECT 1 FROM purchase_orders po WHERE po.budget_id = b.id);

DELETE FROM items i
WHERE i.id LIKE 'ITM-%'
  AND NOT EXISTS (SELECT 1 FROM po_lines  l WHERE l.item_id = i.id)
  AND NOT EXISTS (SELECT 1 FROM grn_lines g WHERE g.item_id = i.id);

DELETE FROM vendors v
WHERE v.id LIKE 'V-0%'
  AND NOT EXISTS (SELECT 1 FROM items            i  WHERE i.preferred_vendor_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM purchase_orders  po WHERE po.vendor_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM invoices         n  WHERE n.vendor_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM grns             g  WHERE g.vendor_id = v.id)
  -- rfqs has no vendor_id: it links vendors through the winner FK and through the
  -- invited_vendor_ids array, which no foreign key protects. Both count as a
  -- reference, so a vendor merely invited to an RFQ is kept.
  AND NOT EXISTS (SELECT 1 FROM rfqs             q  WHERE q.winner_vendor_id = v.id
                                                      OR v.id = ANY (q.invited_vendor_ids))
  AND NOT EXISTS (SELECT 1 FROM rfq_quotes       qq WHERE qq.vendor_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM contracts        c  WHERE c.vendor_id = v.id)
  AND NOT EXISTS (SELECT 1 FROM payments         p  WHERE p.vendor_id = v.id);

-- ---- the demo login --------------------------------------------------------
-- rbac.Seed used to bootstrap viewer@iag.local alongside admin@iag.local, with the
-- hard-coded password "viewer123". It no longer creates it; remove the account from
-- databases that already have it. The Viewers group and its permissions stay — they are
-- authorisation configuration and a real user can be assigned to them. admin@iag.local
-- is a superuser account and is deliberately untouched.
-- auth_user_groups and auth_user_permissions cascade from auth_users; api_audit_logs
-- does not, so detach its rows first rather than losing the audit trail with the account.
UPDATE api_audit_logs SET user_id = NULL
WHERE user_id IN (SELECT id FROM auth_users WHERE email = 'viewer@iag.local' AND is_superuser = FALSE);

DELETE FROM auth_users WHERE email = 'viewer@iag.local' AND is_superuser = FALSE;
