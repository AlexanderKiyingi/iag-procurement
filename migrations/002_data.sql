-- Seed data (same domain as procurement-web/lib/mock-data.ts)

INSERT INTO vendors (id, name, logo, category, contact, email, phone, country, terms, rating, status, total_spend, open_pos) VALUES
('V-001', 'Bukenya Coffee Farms', 'BC', 'Green Coffee Suppliers', 'Joseph Bukenya', 'joseph@bukenyafarms.ug', '+256 772 410 882', 'Uganda', 'Net 30', 4.8, 'Active', 124500, 3),
('V-002', 'Sironko Cooperative Union', 'SC', 'Green Coffee Suppliers', 'Esther Nakato', 'finance@sironkocoop.ug', '+256 758 220 117', 'Uganda', 'Net 45', 4.5, 'Active', 88200, 2),
('V-003', 'Probat Roasters Ltd', 'PR', 'Equipment & Machinery', 'Markus Wagner', 'sales@probat-uganda.com', '+49 2871 3924 0', 'Germany', '50% advance, 50% on delivery', 5.0, 'Active', 215000, 1),
('V-004', 'Mukwano Industries', 'MI', 'Packaging & Labels', 'Rakesh Patel', 'sales@mukwano.com', '+256 414 254 100', 'Uganda', 'Net 30', 4.2, 'Active', 42800, 4),
('V-005', 'Bulambuli Farmers Group', 'BF', 'Green Coffee Suppliers', 'Wycliffe Masaba', 'wycliffe@bulambulifarmers.ug', '+256 701 552 803', 'Uganda', 'Net 14', 4.6, 'Active', 67400, 2),
('V-006', 'GTC Bag Manufacturers', 'GT', 'Packaging & Labels', 'Sarah Nambi', 'orders@gtcbags.com', '+256 414 200 882', 'Uganda', 'Net 30', 4.4, 'Active', 33200, 1),
('V-007', 'EcoTech Quality Lab Supplies', 'ET', 'Lab & QA Equipment', 'David Otim', 'david@ecotech.co.ke', '+254 720 884 110', 'Kenya', 'Net 30', 4.7, 'Active', 28900, 1),
('V-008', 'Total Energies Uganda', 'TE', 'Utilities & Fuel', 'Account Manager', 'corporate@totalenergies.ug', '+256 414 233 850', 'Uganda', 'Net 15', 4.0, 'Active', 54200, 6),
('V-009', 'Jacobs Douwe Egberts', 'JD', 'Export Brokers', 'Henrik Jansen', 'h.jansen@jdecoffee.com', '+31 20 558 1755', 'Netherlands', 'LC at sight', 4.9, 'Active', 0, 0),
('V-010', 'Kapchorwa Tooling Workshop', 'KT', 'Spare Parts & Maintenance', 'Robert Cherop', 'robert.cherop@gmail.com', '+256 772 998 011', 'Uganda', 'Cash on delivery', 3.9, 'Active', 8200, 2)
ON CONFLICT (id) DO NOTHING;

INSERT INTO items (id, sku, name, category, uom, stock, reorder, last_price, currency, preferred_vendor_id) VALUES
('ITM-001', 'GRN-BUG-AA', 'Green Coffee — Bugisu AA', 'Raw Materials', 'kg', 8400, 2000, 5.4, 'USD', 'V-001'),
('ITM-002', 'GRN-BUG-AB', 'Green Coffee — Bugisu AB', 'Raw Materials', 'kg', 6200, 1500, 4.85, 'USD', 'V-001'),
('ITM-003', 'GRN-SF-NAT', 'Green Coffee — Sipi Falls Natural', 'Raw Materials', 'kg', 3100, 1000, 6.2, 'USD', 'V-002'),
('ITM-011', 'FUEL-DSL-001', 'Diesel Fuel (factory generator)', 'Utilities', 'L', 2400, 1000, 1.42, 'USD', 'V-008'),
('ITM-005', 'PKG-JUTE-60', 'Jute Bags 60kg (export grade)', 'Packaging', 'pcs', 1240, 500, 3.2, 'USD', 'V-006'),
('ITM-013', 'PKG-CTN-EXP', 'Export Carton 24kg', 'Packaging', 'pcs', 840, 300, 1.85, 'USD', 'V-006'),
('ITM-008', 'EQP-RST-120', 'Probat P120 Roaster Spare Drum', 'Equipment', 'pcs', 1, 1, 8400, 'USD', 'V-003')
ON CONFLICT (id) DO NOTHING;

INSERT INTO budgets (id, code, period, allocated, committed, spent, remaining, dept) VALUES
('BDG-2026-RM', '4100 — Raw Materials', 'FY2026', 850000, 412800, 298400, 138800, 'Production'),
('BDG-2026-PK', '4200 — Packaging', 'FY2026', 120000, 48200, 32100, 39700, 'Production'),
('BDG-2026-EQ', '4300 — Equipment & CapEx', 'FY2026', 280000, 215000, 0, 65000, 'Engineering'),
('BDG-2026-LB', '4400 — Lab & QA', 'FY2026', 45000, 18800, 11200, 15000, 'Quality'),
('BDG-2026-UT', '4500 — Utilities', 'FY2026', 90000, 32400, 28100, 29500, 'Operations'),
('BDG-2026-MR', '4600 — Maintenance', 'FY2026', 55000, 8200, 6400, 40400, 'Engineering')
ON CONFLICT (id) DO NOTHING;

INSERT INTO requisitions (id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id) VALUES
('PR-2026-0042', 'Green coffee restock — Bugisu AA', 'Production', 'James Okello', 'High', 'Approved', '2026-04-22', '2026-05-15', 21600, 'USD', 'BDG-2026-RM'),
('PR-2026-0043', 'Lab moisture meter replacement', 'Quality', 'Patricia Akello', 'Medium', 'Pending Approval', '2026-04-23', '2026-05-30', 1280, 'USD', 'BDG-2026-LB'),
('PR-2026-0044', 'Export packaging — Japan shipment', 'Production', 'James Okello', 'High', 'Approved', '2026-04-23', '2026-05-10', 5160, 'USD', 'BDG-2026-PK'),
('PR-2026-0045', 'V-belts and mill spares', 'Engineering', 'Robert Acam', 'Low', 'Draft', '2026-04-24', '2026-06-01', 248, 'USD', 'BDG-2026-MR'),
('PR-2026-0046', 'Diesel restock', 'Operations', 'Henry Wamala', 'High', 'Pending Approval', '2026-04-24', '2026-04-28', 2840, 'USD', 'BDG-2026-UT')
ON CONFLICT (id) DO NOTHING;

INSERT INTO rfqs (id, title, status, due_date, created_at, winner_vendor_id, invited_vendor_ids) VALUES
('RFQ-2026-0011', 'Green coffee Q2 — Bugisu AA 4t', 'Closed', '2026-04-20', '2026-04-12', 'V-001', ARRAY['V-001','V-002','V-005']::text[]),
('RFQ-2026-0012', 'Roaster spare drum — Probat P120', 'Awarded', '2026-04-15', '2026-03-20', 'V-003', ARRAY['V-003']::text[]),
('RFQ-2026-0013', 'Export carton bid — Q2 volume', 'Open', '2026-05-02', '2026-04-22', NULL, ARRAY['V-004','V-006']::text[]),
('RFQ-2026-0014', 'Diesel annual contract', 'Open', '2026-05-10', '2026-04-25', NULL, ARRAY['V-008']::text[])
ON CONFLICT (id) DO NOTHING;

INSERT INTO purchase_orders (id, vendor_id, title, total, currency, status, created_at, expected_date, budget_id) VALUES
('PO-2026-0117', 'V-002', 'Sipi Falls Natural 1.5t', 9300, 'USD', 'In Transit', '2026-04-21', '2026-05-20', 'BDG-2026-RM'),
('PO-2026-0118', 'V-001', 'Bugisu AA 4t restock', 21800, 'USD', 'Approved', '2026-04-22', '2026-05-15', 'BDG-2026-RM'),
('PO-2026-0119', 'V-006', 'Export packaging bundle', 5250, 'USD', 'Sent to Vendor', '2026-04-23', '2026-05-10', 'BDG-2026-PK'),
('PO-2026-0116', 'V-003', 'Probat P120 spare drum', 10500, 'USD', 'Received', '2026-03-22', '2026-04-22', 'BDG-2026-EQ')
ON CONFLICT (id) DO NOTHING;

INSERT INTO po_lines (po_id, item_id, qty, unit_price) VALUES
('PO-2026-0117', 'ITM-003', 1500, 6.2),
('PO-2026-0118', 'ITM-001', 4000, 5.4),
('PO-2026-0119', 'ITM-005', 1500, 3.2),
('PO-2026-0119', 'ITM-013', 200, 1.85),
('PO-2026-0116', 'ITM-008', 1, 8400);

INSERT INTO grns (id, po_id, vendor_id, received_date, received_by, status) VALUES
('GRN-2026-0089', 'PO-2026-0116', 'V-003', '2026-04-22', 'Robert Acam', 'Posted'),
('GRN-2026-0088', 'PO-2026-0115', 'V-008', '2026-03-16', 'Henry Wamala', 'Posted'),
('GRN-2026-0087', 'PO-2026-0114', 'V-005', '2026-03-22', 'James Okello', 'Posted')
ON CONFLICT (id) DO NOTHING;

INSERT INTO invoices (id, invoice_no, vendor_id, po_id, amount, currency, status, match_status, invoice_date) VALUES
('INV-V003-44218', NULL, 'V-003', 'PO-2026-0116', 10500, 'USD', 'Paid', '3-way matched', '2026-04-22'),
('INV-V008-4421', NULL, 'V-008', 'PO-2026-0115', 2130, 'USD', 'Paid', '3-way matched', '2026-03-17'),
('INV-V005-1102', NULL, 'V-005', 'PO-2026-0114', 11472, 'USD', 'Pending Approval', 'Quantity variance', '2026-03-23'),
('INV-V002-2210', NULL, 'V-002', 'PO-2026-0117', 9300, 'USD', 'On Hold', 'Pending GRN', '2026-04-23'),
('INV-V004-9982', NULL, 'V-004', 'PO-2026-0113', 5185, 'USD', 'Paid', '3-way matched', '2026-03-16'),
('INV-V001-8801', NULL, 'V-001', 'PO-2026-0112', 9850, 'USD', 'Paid', '3-way matched', '2026-03-09'),
('INV-V010-1144', NULL, 'V-010', NULL, 380, 'USD', 'Pending Approval', 'No PO', '2026-04-24')
ON CONFLICT (id) DO NOTHING;

INSERT INTO contracts (id, vendor_id, title, start_date, end_date, value, currency, status) VALUES
('CNT-2026-001', 'V-001', 'Bukenya Farms — Annual Green Coffee Supply 2026', '2026-01-15', '2026-12-31', 180000, 'USD', 'Active'),
('CNT-2026-002', 'V-008', 'Total Energies — Diesel Supply Agreement', '2026-01-01', '2026-12-31', 48000, 'USD', 'Active'),
('CNT-2026-003', 'V-009', 'JDE — Export Distribution Agreement', '2026-03-01', '2027-02-28', 520000, 'USD', 'Active'),
('CNT-2025-014', 'V-003', 'Probat — Equipment Service Contract', '2025-08-01', '2026-07-31', 24000, 'USD', 'Expiring Soon')
ON CONFLICT (id) DO NOTHING;

INSERT INTO payments (id, invoice_id, vendor_id, amount, currency, pay_date, method, reference, status, initiated_by) VALUES
('PAY-2026-0234', 'INV-V003-44218', 'V-003', 10500, 'USD', '2026-04-25', 'SWIFT', 'SWIFT-44218-DE', 'Cleared', 'Daniel K.'),
('PAY-2026-0220', 'INV-V004-9982', 'V-004', 5185, 'USD', '2026-04-12', 'EFT', 'STBC-EFT-9982', 'Cleared', 'Daniel K.'),
('PAY-2026-0212', 'INV-V001-8801', 'V-001', 9850, 'USD', '2026-04-05', 'EFT', 'STBC-EFT-8801', 'Cleared', 'Daniel K.')
ON CONFLICT (id) DO NOTHING;

INSERT INTO audit_entries (id, ts, username, action, target, detail) VALUES
(1, '2026-04-26 09:14:00', 'Menelik K.', 'Created PR', 'PR-2026-0050', 'Bulambuli washed lot, $7,600'),
(2, '2026-04-25 16:32:00', 'Sarah M.', 'Approved PO', 'PO-2026-0118', 'Bugisu AA 4t — $21,800'),
(3, '2026-04-25 14:08:00', 'Daniel K.', 'Issued Payment', 'PAY-2026-0234', 'SWIFT to Probat — $10,500'),
(4, '2026-04-25 11:22:00', 'Patricia A.', 'Created PR', 'PR-2026-0049', 'SCA cupping set'),
(5, '2026-04-25 10:11:00', 'James O.', 'Submitted GRN', 'GRN-2026-0089', 'Probat drum installed')
ON CONFLICT (id) DO NOTHING;

SELECT setval(pg_get_serial_sequence('audit_entries', 'id'), COALESCE((SELECT MAX(id) FROM audit_entries), 1));
