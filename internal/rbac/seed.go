package rbac

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Arbitrary pair — serializes rbac.Seed across concurrent API processes (e.g. multiple pods on first deploy).
const seedAdvisoryLockKey1 int32 = 884921403
const seedAdvisoryLockKey2 int32 = 173959502

type permDef struct {
	code, name, desc string
}

var bootstrapPermissions = []permDef{
	{ViewUser, "Can view user", "View user accounts"},
	{ChangeUser, "Can change user", "Create or update users"},
	{ViewGroup, "Can view group", "View permission groups"},
	{ChangeGroup, "Can change group", "Create or update groups"},
	{ViewPermission, "Can view permission", "List permission catalog"},
	{ViewSeed, "Can view procurement seed", "Read master / seed API payloads"},
	{AddRequisition, "Can add requisition", "Create purchase requisitions"},
	{ChangeRequisition, "Can change requisition", "Update purchase requisitions"},
	{DeleteRequisition, "Can delete requisition", "Delete purchase requisitions"},
	{AddPurchaseOrder, "Can add purchase order", "Create purchase orders with lines"},
	{ChangePurchaseOrder, "Can change purchase order", "Update purchase orders"},
	{DeletePurchaseOrder, "Can delete purchase order", "Delete purchase orders"},
	{AddVendor, "Can add vendor", "Create vendor records"},
	{ChangeVendor, "Can change vendor", "Update vendor records"},
	{DeleteVendor, "Can delete vendor", "Delete vendor records"},
	{AddItem, "Can add item", "Create catalog items"},
	{ChangeItem, "Can change item", "Update catalog items"},
	{DeleteItem, "Can delete item", "Delete catalog items"},
	{AddBudget, "Can add budget", "Create budget envelopes"},
	{ChangeBudget, "Can change budget", "Update budget envelopes"},
	{DeleteBudget, "Can delete budget", "Delete budget envelopes"},
	{AddRfq, "Can add RFQ", "Create requests for quotation"},
	{ChangeRfq, "Can change RFQ", "Update requests for quotation"},
	{DeleteRfq, "Can delete RFQ", "Delete requests for quotation"},
	{AddGrn, "Can add GRN", "Record goods receipts"},
	{ChangeGrn, "Can change GRN", "Update goods receipts"},
	{DeleteGrn, "Can delete GRN", "Delete goods receipts"},
	{AddInvoice, "Can add invoice", "Capture vendor invoices"},
	{ChangeInvoice, "Can change invoice", "Update vendor invoices"},
	{DeleteInvoice, "Can delete invoice", "Delete vendor invoices"},
	{AddContract, "Can add contract", "Create vendor contracts"},
	{ChangeContract, "Can change contract", "Update vendor contracts"},
	{DeleteContract, "Can delete contract", "Delete vendor contracts"},
	{ViewAPIAudit, "Can view API audit log", "Read HTTP audit entries"},
	{ViewInbox, "Can view notifications", "Read in-app notification inbox"},
	{EmitNotification, "Can emit demo notifications", "Trigger signal / email demos"},
}

// Seed installs default permissions, groups, and bootstrap users when auth_users is empty.
// Uses a transaction-scoped advisory lock so only one replica seeds at a time; others wait and then see users exist.
func Seed(ctx context.Context, pool *pgxpool.Pool, defaultAdminPassword string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, seedAdvisoryLockKey1, seedAdvisoryLockKey2); err != nil {
		return err
	}

	var n int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM auth_users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		committed = true
		return nil
	}

	if defaultAdminPassword == "" {
		defaultAdminPassword = "admin123"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	viewerPass := "viewer123"
	hashViewer, err := bcrypt.GenerateFromPassword([]byte(viewerPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	for _, p := range bootstrapPermissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_permissions (code, name, description)
			VALUES ($1, $2, $3) ON CONFLICT (code) DO NOTHING`, p.code, p.name, p.desc); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_groups (name, description) VALUES
		('Administrators', 'Full access (Django-style superuser group)'),
		('Viewers', 'Read-only procurement and audit access')
		ON CONFLICT (name) DO NOTHING`); err != nil {
		return err
	}

	var adminGroupID, viewerGroupID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM auth_groups WHERE name = 'Administrators'`).Scan(&adminGroupID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM auth_groups WHERE name = 'Viewers'`).Scan(&viewerGroupID); err != nil {
		return err
	}

	for _, p := range bootstrapPermissions {
		var pid int64
		if err := tx.QueryRow(ctx, `SELECT id FROM auth_permissions WHERE code = $1`, p.code).Scan(&pid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_group_permissions (group_id, permission_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, adminGroupID, pid); err != nil {
			return err
		}
	}
	viewOnly := []string{ViewSeed, ViewAPIAudit, ViewInbox}
	for _, code := range viewOnly {
		var pid int64
		if err := tx.QueryRow(ctx, `SELECT id FROM auth_permissions WHERE code = $1`, code).Scan(&pid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_group_permissions (group_id, permission_id)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, viewerGroupID, pid); err != nil {
			return err
		}
	}

	var adminUID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_users (email, password_hash, is_superuser)
		VALUES ($1, $2, TRUE) RETURNING id`,
		"admin@iag.local", string(hash)).Scan(&adminUID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth_user_groups (user_id, group_id) VALUES ($1, $2)`, adminUID, adminGroupID); err != nil {
		return err
	}

	var viewerUID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_users (email, password_hash, is_superuser)
		VALUES ($1, $2, FALSE) RETURNING id`,
		"viewer@iag.local", string(hashViewer)).Scan(&viewerUID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO auth_user_groups (user_id, group_id) VALUES ($1, $2)`, viewerUID, viewerGroupID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	log.Printf("rbac: bootstrapped admin@iag.local and viewer@iag.local (dev passwords — change in production)")
	return nil
}
