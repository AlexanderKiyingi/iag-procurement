-- Track which PM workspace originated each imported requisition, so approval
-- and rejection events can be routed back via workspaceOwnerUserId.

ALTER TABLE requisitions
    ADD COLUMN IF NOT EXISTS pm_workspace_owner TEXT;
