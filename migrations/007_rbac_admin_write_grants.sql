-- Ensure Administrators can mutate RBAC (existing seeds usually grant this already).

INSERT INTO auth_group_permissions (group_id, permission_id)
SELECT g.id, p.id
FROM auth_groups g
JOIN auth_permissions p ON p.code IN ('auth.change_user', 'auth.change_group')
WHERE g.name = 'Administrators'
ON CONFLICT (group_id, permission_id) DO NOTHING;
