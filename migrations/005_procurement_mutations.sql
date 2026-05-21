-- Permissions for procurement mutations (POST requisitions / purchase orders).

INSERT INTO auth_permissions (code, name, description) VALUES
('procurement.add_requisition', 'Can add requisition', 'Create purchase requisitions'),
('procurement.add_purchase_order', 'Can add purchase order', 'Create purchase orders with lines')
ON CONFLICT (code) DO NOTHING;

INSERT INTO auth_group_permissions (group_id, permission_id)
SELECT g.id, p.id
FROM auth_groups g
JOIN auth_permissions p ON p.code IN ('procurement.add_requisition', 'procurement.add_purchase_order')
WHERE g.name = 'Administrators'
ON CONFLICT DO NOTHING;
