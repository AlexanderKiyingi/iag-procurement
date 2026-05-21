-- Permissions for additional procurement writes (vendors, items, budgets, RFQs, GRNs, invoices, contracts).

INSERT INTO auth_permissions (code, name, description) VALUES
('procurement.add_vendor', 'Can add vendor', 'Create vendor records'),
('procurement.add_item', 'Can add item', 'Create catalog items'),
('procurement.add_budget', 'Can add budget', 'Create budget envelopes'),
('procurement.add_rfq', 'Can add RFQ', 'Create requests for quotation'),
('procurement.add_grn', 'Can add GRN', 'Record goods receipts'),
('procurement.add_invoice', 'Can add invoice', 'Capture vendor invoices'),
('procurement.add_contract', 'Can add contract', 'Create vendor contracts')
ON CONFLICT (code) DO NOTHING;

INSERT INTO auth_group_permissions (group_id, permission_id)
SELECT g.id, p.id
FROM auth_groups g
JOIN auth_permissions p ON p.code IN (
  'procurement.add_vendor',
  'procurement.add_item',
  'procurement.add_budget',
  'procurement.add_rfq',
  'procurement.add_grn',
  'procurement.add_invoice',
  'procurement.add_contract'
)
WHERE g.name = 'Administrators'
ON CONFLICT (group_id, permission_id) DO NOTHING;
