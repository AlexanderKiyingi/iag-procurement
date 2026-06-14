package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

// UpdateItem applies partial update. preferredVendorID: if non-nil and points to "" => clear.
func (p *Procurement) UpdateItem(
	ctx context.Context,
	id string,
	sku, name, category, uom *string,
	stock, reorder, lastPrice *float64,
	currency *string,
	preferredVendorID *string,
	auditUser string,
) (*models.Item, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if sku != nil && strings.TrimSpace(*sku) == "" {
		return nil, fmt.Errorf("%w: sku is required", ErrInvalidArgument)
	}
	if name != nil && strings.TrimSpace(*name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}

	var pref interface{}
	if preferredVendorID == nil {
		pref = nil // no change
	} else if strings.TrimSpace(*preferredVendorID) == "" {
		pref = (*string)(nil) // clear
	} else {
		s := strings.TrimSpace(*preferredVendorID)
		pref = s
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE items SET
			sku = COALESCE($2, sku),
			name = COALESCE($3, name),
			category = COALESCE($4, category),
			uom = COALESCE($5, uom),
			stock = COALESCE($6, stock),
			reorder = COALESCE($7, reorder),
			last_price = COALESCE($8, last_price),
			currency = COALESCE($9, currency),
			preferred_vendor_id = CASE
				WHEN $10::text IS NULL THEN preferred_vendor_id
				ELSE $10::text
			END
		WHERE id = $1`,
		id, sku, name, category, uom, stock, reorder, lastPrice, currency, pref,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "item updated",
	); err != nil {
		return nil, err
	}

	var out models.Item
	var prefOut *string
	if err := tx.QueryRow(ctx, `
		SELECT id, sku, name, category, uom, stock, reorder, last_price, currency, preferred_vendor_id
		FROM items WHERE id = $1`, id,
	).Scan(&out.ID, &out.SKU, &out.Name, &out.Category, &out.UOM, &out.Stock, &out.Reorder, &out.LastPrice, &out.Currency, &prefOut); err != nil {
		return nil, err
	}
	if prefOut != nil {
		out.PreferredVendor = strings.TrimSpace(*prefOut)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteItem(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM items WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "item deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Procurement) UpdateBudget(ctx context.Context, id string, code, period *string, allocated *float64, dept *string, auditUser string) (*models.Budget, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if code != nil && strings.TrimSpace(*code) == "" {
		return nil, fmt.Errorf("%w: code is required", ErrInvalidArgument)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Update budget, and if allocated changes, recompute remaining based on existing committed/spent.
	// remaining = allocated - committed - spent
	ct, err := tx.Exec(ctx, `
		UPDATE budgets b SET
			code = COALESCE($2, code),
			period = COALESCE($3, period),
			allocated = COALESCE($4, allocated),
			dept = COALESCE($5, dept),
			remaining = (COALESCE($4, allocated) - committed - spent)
		WHERE id = $1`,
		id, code, period, allocated, dept,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "budget updated",
	); err != nil {
		return nil, err
	}

	var out models.Budget
	if err := tx.QueryRow(ctx, `
		SELECT id, code, period, allocated, committed, spent, remaining, dept FROM budgets WHERE id = $1`, id,
	).Scan(&out.ID, &out.Code, &out.Period, &out.Allocated, &out.Committed, &out.Spent, &out.Remaining, &out.Dept); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteBudget(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM budgets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "budget deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateRfq supports replacing invited list. dueDate: if present and nil => clear. winnerVendorID: if non-nil and points to "" => clear.
func (p *Procurement) UpdateRfq(
	ctx context.Context,
	id string,
	title, status *string,
	dueDate **time.Time,
	winnerVendorID *string,
	invitedVendorIDs *[]string,
	auditUser string,
) (*models.Rfq, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if title != nil && strings.TrimSpace(*title) == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}

	var dueArg interface{}
	if dueDate == nil {
		dueArg = nil // no change
	} else if *dueDate == nil {
		dueArg = (*time.Time)(nil) // clear
	} else {
		dueArg = **dueDate
	}
	var winnerArg interface{}
	if winnerVendorID == nil {
		winnerArg = nil // no change
	} else if strings.TrimSpace(*winnerVendorID) == "" {
		winnerArg = (*string)(nil) // clear
	} else {
		w := strings.TrimSpace(*winnerVendorID)
		winnerArg = w
	}
	var invitedArg interface{}
	if invitedVendorIDs == nil {
		invitedArg = nil // no change
	} else {
		invitedArg = *invitedVendorIDs
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE rfqs SET
			title = COALESCE($2, title),
			status = COALESCE($3, status),
			due_date = CASE WHEN $4::date IS NULL THEN due_date ELSE $4::date END,
			winner_vendor_id = CASE WHEN $5::text IS NULL THEN winner_vendor_id ELSE $5::text END,
			invited_vendor_ids = CASE WHEN $6::text[] IS NULL THEN invited_vendor_ids ELSE $6::text[] END
		WHERE id = $1`,
		id, title, status, dueArg, winnerArg, invitedArg,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "rfq updated",
	); err != nil {
		return nil, err
	}

	var out models.Rfq
	var due, created *time.Time
	var winner *string
	if err := tx.QueryRow(ctx, `
		SELECT id, title, status, due_date, created_at, winner_vendor_id, invited_vendor_ids
		FROM rfqs WHERE id = $1`, id,
	).Scan(&out.ID, &out.Title, &out.Status, &due, &created, &winner, &out.InvitedVendors); err != nil {
		return nil, err
	}
	out.DueDate = ""
	out.CreatedAt = ""
	if due != nil {
		out.DueDate = due.UTC().Format("2006-01-02")
	}
	if created != nil {
		out.CreatedAt = created.UTC().Format("2006-01-02")
	}
	out.WinnerVendor = winner

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteRfq(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `DELETE FROM rfqs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "rfq deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateContract start/end: if present and nil => clear.
func (p *Procurement) UpdateContract(
	ctx context.Context,
	id string,
	vendorID, title *string,
	startDate, endDate **time.Time,
	value *float64,
	currency, status *string,
	auditUser string,
) (*models.Contract, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if vendorID != nil && strings.TrimSpace(*vendorID) == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}
	if title != nil && strings.TrimSpace(*title) == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}
	var sdArg interface{}
	if startDate == nil {
		sdArg = nil
	} else if *startDate == nil {
		sdArg = (*time.Time)(nil)
	} else {
		sdArg = **startDate
	}
	var edArg interface{}
	if endDate == nil {
		edArg = nil
	} else if *endDate == nil {
		edArg = (*time.Time)(nil)
	} else {
		edArg = **endDate
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE contracts SET
			vendor_id = COALESCE($2, vendor_id),
			title = COALESCE($3, title),
			start_date = CASE WHEN $4::date IS NULL THEN start_date ELSE $4::date END,
			end_date = CASE WHEN $5::date IS NULL THEN end_date ELSE $5::date END,
			value = COALESCE($6, value),
			currency = COALESCE($7, currency),
			status = COALESCE($8, status)
		WHERE id = $1`,
		id, vendorID, title, sdArg, edArg, value, currency, status,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "contract updated",
	); err != nil {
		return nil, err
	}

	var out models.Contract
	var sd, ed *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id, vendor_id, title, start_date, end_date, value, currency, status
		FROM contracts WHERE id = $1`, id,
	).Scan(&out.ID, &out.VendorID, &out.Title, &sd, &ed, &out.Value, &out.Currency, &out.Status); err != nil {
		return nil, err
	}
	if sd != nil {
		out.StartDate = sd.UTC().Format("2006-01-02")
	}
	if ed != nil {
		out.EndDate = ed.UTC().Format("2006-01-02")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteContract(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `DELETE FROM contracts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "contract deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateInvoice supports clearing invoiceNo, poId, invoiceDate by passing pointers to "" (handlers do this).
func (p *Procurement) UpdateInvoice(
	ctx context.Context,
	id string,
	invoiceNo, vendorID, poID *string,
	amount *float64,
	currency, status, matchStatus *string,
	invoiceDate **time.Time,
	auditUser string,
) (*models.Invoice, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if vendorID != nil && strings.TrimSpace(*vendorID) == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}

	var invNoArg interface{}
	if invoiceNo == nil {
		invNoArg = nil
	} else if strings.TrimSpace(*invoiceNo) == "" {
		invNoArg = (*string)(nil)
	} else {
		s := strings.TrimSpace(*invoiceNo)
		invNoArg = s
	}
	var poArg interface{}
	if poID == nil {
		poArg = nil
	} else if strings.TrimSpace(*poID) == "" {
		poArg = (*string)(nil)
	} else {
		s := strings.TrimSpace(*poID)
		poArg = s
	}
	var dateArg interface{}
	if invoiceDate == nil {
		dateArg = nil
	} else if *invoiceDate == nil {
		dateArg = (*time.Time)(nil)
	} else {
		dateArg = **invoiceDate
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE invoices SET
			invoice_no = CASE WHEN $2::text IS NULL THEN invoice_no ELSE $2::text END,
			vendor_id = COALESCE($3, vendor_id),
			po_id = CASE WHEN $4::text IS NULL THEN po_id ELSE $4::text END,
			amount = COALESCE($5, amount),
			currency = COALESCE($6, currency),
			status = COALESCE($7, status),
			match_status = COALESCE($8, match_status),
			invoice_date = CASE WHEN $9::date IS NULL THEN invoice_date ELSE $9::date END
		WHERE id = $1`,
		id, invNoArg, vendorID, poArg, amount, currency, status, matchStatus, dateArg,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "invoice updated",
	); err != nil {
		return nil, err
	}

	var out models.Invoice
	var invNoOut *string
	var poOut *string
	var dt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id, invoice_no, vendor_id, po_id, amount, currency, status, match_status, invoice_date
		FROM invoices WHERE id = $1`, id,
	).Scan(&out.ID, &invNoOut, &out.VendorID, &poOut, &out.Amount, &out.Currency, &out.Status, &out.MatchStatus, &dt); err != nil {
		return nil, err
	}
	out.InvoiceNo = invNoOut
	out.PoID = poOut
	if dt != nil {
		out.InvoiceDate = dt.UTC().Format("2006-01-02")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteInvoice(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "invoice deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdatePurchaseOrder supports replacing lines. expectedDate: if present and nil => clear.
func (p *Procurement) UpdatePurchaseOrder(
	ctx context.Context,
	id string,
	vendorID, title, currency, status *string,
	expectedDate **time.Time,
	budgetID *string,
	lines *[]models.PoLine,
	auditUser string,
) (*models.Po, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if vendorID != nil && strings.TrimSpace(*vendorID) == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}
	if title != nil && strings.TrimSpace(*title) == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}
	if budgetID != nil && strings.TrimSpace(*budgetID) == "" {
		return nil, fmt.Errorf("%w: budgetId cannot be blank", ErrInvalidArgument)
	}
	if lines != nil && len(*lines) == 0 {
		return nil, fmt.Errorf("%w: at least one line item is required", ErrInvalidArgument)
	}

	var exArg interface{}
	if expectedDate == nil {
		exArg = nil
	} else if *expectedDate == nil {
		exArg = (*time.Time)(nil)
	} else {
		exArg = **expectedDate
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Load current vendor id (to adjust open_pos if vendor changes) and the
	// creator (for the segregation-of-duties check below).
	var curVendor, createdBy string
	if err := tx.QueryRow(ctx, `SELECT vendor_id, COALESCE(created_by, '') FROM purchase_orders WHERE id = $1`, id).Scan(&curVendor, &createdBy); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Segregation of duties: the creator may not approve their own PO.
	if status != nil && strings.EqualFold(strings.TrimSpace(*status), "approved") && sameActor(auditUser, createdBy) {
		return nil, fmt.Errorf("%w: creator cannot approve their own purchase order", ErrForbidden)
	}

	// If lines are provided, replace and recompute total.
	var totalPtr *float64
	if lines != nil {
		var total float64
		for _, ln := range *lines {
			if ln.Qty <= 0 || ln.Price < 0 || strings.TrimSpace(ln.ItemID) == "" {
				return nil, fmt.Errorf("%w: invalid line qty/price/itemId", ErrInvalidArgument)
			}
			total += ln.Qty * ln.Price
		}
		totalPtr = &total
		if _, err := tx.Exec(ctx, `DELETE FROM po_lines WHERE po_id = $1`, id); err != nil {
			return nil, err
		}
		for _, ln := range *lines {
			if _, err := tx.Exec(ctx, `INSERT INTO po_lines (po_id, item_id, qty, unit_price) VALUES ($1,$2,$3,$4)`,
				id, strings.TrimSpace(ln.ItemID), ln.Qty, ln.Price,
			); err != nil {
				return nil, err
			}
		}
	}

	newVendor := curVendor
	if vendorID != nil {
		newVendor = strings.TrimSpace(*vendorID)
	}

	ct, err := tx.Exec(ctx, `
		UPDATE purchase_orders SET
			vendor_id = COALESCE($2, vendor_id),
			title = COALESCE($3, title),
			total = COALESCE($4, total),
			currency = COALESCE($5, currency),
			status = COALESCE($6, status),
			expected_date = CASE WHEN $7::date IS NULL THEN expected_date ELSE $7::date END,
			budget_id = COALESCE($8, budget_id)
		WHERE id = $1`,
		id, vendorID, title, totalPtr, currency, status, exArg, budgetID,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	// Adjust vendor open_pos if vendor changed.
	if newVendor != curVendor {
		if _, err := tx.Exec(ctx, `UPDATE vendors SET open_pos = GREATEST(open_pos - 1, 0) WHERE id = $1`, curVendor); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE vendors SET open_pos = open_pos + 1 WHERE id = $1`, newVendor); err != nil {
			return nil, err
		}
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "purchase order updated",
	); err != nil {
		return nil, err
	}

	// Build output
	var out models.Po
	var ca, ex *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id, vendor_id, title, total, currency, status, created_at, expected_date, budget_id
		FROM purchase_orders WHERE id = $1`, id,
	).Scan(&out.ID, &out.VendorID, &out.Title, &out.Total, &out.Currency, &out.Status, &ca, &ex, &out.BudgetID); err != nil {
		return nil, err
	}
	if ca != nil {
		out.CreatedAt = ca.UTC().Format("2006-01-02")
	}
	if ex != nil {
		out.ExpectedDate = ex.UTC().Format("2006-01-02")
	}
	out.Items = []models.PoLine{}
	rows, err := tx.Query(ctx, `SELECT item_id, qty, unit_price FROM po_lines WHERE po_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var itemID string
		var qty, price float64
		if err := rows.Scan(&itemID, &qty, &price); err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, models.PoLine{ItemID: itemID, Qty: qty, Price: price})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeletePurchaseOrder(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var vendorID string
	if err := tx.QueryRow(ctx, `SELECT vendor_id FROM purchase_orders WHERE id = $1`, id).Scan(&vendorID); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return err
	}

	ct, err := tx.Exec(ctx, `DELETE FROM purchase_orders WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	if vendorID != "" {
		if _, err := tx.Exec(ctx, `UPDATE vendors SET open_pos = GREATEST(open_pos - 1, 0) WHERE id = $1`, vendorID); err != nil {
			return err
		}
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "purchase order deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateGrn supports clearing poId/receivedDate by passing pointers to "" (handlers do this).
func (p *Procurement) UpdateGrn(
	ctx context.Context,
	id string,
	vendorID, poID *string,
	receivedDate **time.Time,
	receivedBy, status *string,
	auditUser string,
) (*models.Grn, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if vendorID != nil && strings.TrimSpace(*vendorID) == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}

	var poArg interface{}
	if poID == nil {
		poArg = nil
	} else if strings.TrimSpace(*poID) == "" {
		poArg = (*string)(nil)
	} else {
		s := strings.TrimSpace(*poID)
		poArg = s
	}
	var rdArg interface{}
	if receivedDate == nil {
		rdArg = nil
	} else if *receivedDate == nil {
		rdArg = (*time.Time)(nil)
	} else {
		rdArg = **receivedDate
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE grns SET
			po_id = CASE WHEN $2::text IS NULL THEN po_id ELSE $2::text END,
			vendor_id = COALESCE($3, vendor_id),
			received_date = CASE WHEN $4::date IS NULL THEN received_date ELSE $4::date END,
			received_by = COALESCE($5, received_by),
			status = COALESCE($6, status)
		WHERE id = $1`,
		id, poArg, vendorID, rdArg, receivedBy, status,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "grn updated",
	); err != nil {
		return nil, err
	}

	var out models.Grn
	var poOut *string
	var rd *time.Time
	if err := tx.QueryRow(ctx, `SELECT id, po_id, vendor_id, received_date, received_by, status FROM grns WHERE id = $1`, id).
		Scan(&out.ID, &poOut, &out.VendorID, &rd, &out.ReceivedBy, &out.Status); err != nil {
		return nil, err
	}
	out.PoID = poOut
	if rd != nil {
		out.ReceivedDate = rd.UTC().Format("2006-01-02")
	}
	if err := p.recognizePOSpendOnReceipt(ctx, tx, &out); err != nil {
		return nil, err
	}
	if err := p.enqueueGrnPosted(ctx, tx, &out); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteGrn(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `DELETE FROM grns WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "grn deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

