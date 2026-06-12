package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iag-procurement/backend/internal/models"
)

// CreateVendor inserts a vendor master row and audit trail entry.
func (p *Procurement) CreateVendor(ctx context.Context, name, logo, category, contact, email, phone, country, terms string, rating float64, status string, auditUser string) (*models.Vendor, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}
	if status == "" {
		status = "Active"
	}
	id := newProcurementID("V")
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO vendors (id, name, logo, category, contact, email, phone, country, terms, rating, status, total_spend, open_pos)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		id, name, logo, category, contact, email, phone, country, terms, rating, status, 0, 0,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("vendor %s", name),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.Vendor{
		ID: id, Name: name, Logo: logo, Category: category, Contact: contact, Email: email,
		Phone: phone, Country: country, Terms: terms, Rating: rating, Status: status,
		TotalSpend: 0, OpenPOs: 0,
	}, nil
}

// CreateItem inserts a catalog item row and audit trail entry.
func (p *Procurement) CreateItem(ctx context.Context, sku, name, category, uom string, stock, reorder, lastPrice float64, currency, preferredVendorID string, auditUser string) (*models.Item, error) {
	sku = strings.TrimSpace(sku)
	name = strings.TrimSpace(name)
	if sku == "" || name == "" {
		return nil, fmt.Errorf("%w: sku and name are required", ErrInvalidArgument)
	}
	if currency == "" {
		currency = "USD"
	}
	id := newProcurementID("ITM")
	pv := strings.TrimSpace(preferredVendorID)
	var pref interface{}
	if pv != "" {
		pref = pv
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO items (id, sku, name, category, uom, stock, reorder, last_price, currency, preferred_vendor_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, sku, name, category, uom, stock, reorder, lastPrice, currency, pref,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("sku=%s", sku),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := models.Item{
		ID: id, SKU: sku, Name: name, Category: category, UOM: uom,
		Stock: stock, Reorder: reorder, LastPrice: lastPrice, Currency: currency,
	}
	if pv != "" {
		out.PreferredVendor = pv
	}
	return &out, nil
}

// CreateBudget inserts a budget envelope and audit trail entry.
func (p *Procurement) CreateBudget(ctx context.Context, code, period string, allocated float64, dept string, auditUser string) (*models.Budget, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("%w: code is required", ErrInvalidArgument)
	}
	id := newProcurementID("BDG")
	committed, spent := 0.0, 0.0
	remaining := allocated - committed - spent

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO budgets (id, code, period, allocated, committed, spent, remaining, dept)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, code, period, allocated, committed, spent, remaining, dept,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("code=%s allocated=%.2f", code, allocated),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.Budget{
		ID: id, Code: code, Period: period, Allocated: allocated,
		Committed: committed, Spent: spent, Remaining: remaining, Dept: dept,
	}, nil
}

// CreateRfq inserts an RFQ row and audit trail entry.
func (p *Procurement) CreateRfq(ctx context.Context, title string, dueDate *time.Time, invited []string, requisitionID, auditUser string) (*models.Rfq, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}
	if invited == nil {
		invited = []string{}
	}
	id := newProcurementID("RFQ")
	status := "Open"
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO rfqs (id, title, status, due_date, created_at, winner_vendor_id, invited_vendor_ids, requisition_id)
		VALUES ($1,$2,$3,$4,$5,NULL,COALESCE($6::text[], '{}'),NULLIF($7,''))`,
		id, title, status, dueDate, createdDay, invited, strings.TrimSpace(requisitionID),
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("invited=%d", len(invited)),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := models.Rfq{
		ID:             id,
		Title:          title,
		Status:         status,
		CreatedAt:      createdDay.Format("2006-01-02"),
		InvitedVendors: invited,
	}
	if dueDate != nil {
		out.DueDate = dueDate.UTC().Format("2006-01-02")
	}
	return &out, nil
}

// CreateGrn inserts a goods receipt note and audit trail entry.
func (p *Procurement) CreateGrn(ctx context.Context, vendorID string, poID *string, receivedBy, status string, receivedDate *time.Time, auditUser string) (*models.Grn, error) {
	vendorID = strings.TrimSpace(vendorID)
	if vendorID == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}
	if status == "" {
		status = "Draft"
	}
	if strings.TrimSpace(receivedBy) == "" {
		receivedBy = auditUser
	}
	rd := receivedDate
	if rd == nil {
		t := time.Now().UTC()
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		rd = &d
	}
	id := newProcurementID("GRN")

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO grns (id, po_id, vendor_id, received_date, received_by, status)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		id, poID, vendorID, rd, receivedBy, status,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	detail := fmt.Sprintf("vendor=%s", vendorID)
	if poID != nil && *poID != "" {
		detail += fmt.Sprintf(" po=%s", *poID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, detail,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.Grn{
		ID: id, PoID: poID, VendorID: vendorID, ReceivedDate: rd.UTC().Format("2006-01-02"),
		ReceivedBy: receivedBy, Status: status,
	}, nil
}

// CreateInvoice inserts an AP invoice row and audit trail entry.
func (p *Procurement) CreateInvoice(ctx context.Context, vendorID string, poID *string, amount float64, currency string, invoiceDate *time.Time, invoiceNo *string, auditUser string) (*models.Invoice, error) {
	vendorID = strings.TrimSpace(vendorID)
	if vendorID == "" {
		return nil, fmt.Errorf("%w: vendorId is required", ErrInvalidArgument)
	}
	if currency == "" {
		currency = "USD"
	}
	status := "Pending Approval"
	matchStatus := "Pending"
	idate := invoiceDate
	if idate == nil {
		t := time.Now().UTC()
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		idate = &d
	}
	id := newProcurementID("INV")

	var invNo interface{}
	if invoiceNo != nil && strings.TrimSpace(*invoiceNo) != "" {
		s := strings.TrimSpace(*invoiceNo)
		invNo = s
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices (id, invoice_no, vendor_id, po_id, amount, currency, status, match_status, invoice_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, invNo, vendorID, poID, amount, currency, status, matchStatus, idate,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("vendor=%s amount=%.2f %s", vendorID, amount, currency),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := models.Invoice{
		ID: id, VendorID: vendorID, PoID: poID, Amount: amount, Currency: currency,
		Status: status, MatchStatus: matchStatus, InvoiceDate: idate.UTC().Format("2006-01-02"),
	}
	if invoiceNo != nil && strings.TrimSpace(*invoiceNo) != "" {
		s := strings.TrimSpace(*invoiceNo)
		out.InvoiceNo = &s
	}
	return &out, nil
}

// CreateContract inserts a vendor contract row and audit trail entry.
func (p *Procurement) CreateContract(ctx context.Context, vendorID, title string, startDate, endDate *time.Time, value float64, currency, status string, auditUser string) (*models.Contract, error) {
	vendorID = strings.TrimSpace(vendorID)
	title = strings.TrimSpace(title)
	if vendorID == "" || title == "" {
		return nil, fmt.Errorf("%w: vendorId and title are required", ErrInvalidArgument)
	}
	if currency == "" {
		currency = "USD"
	}
	if status == "" {
		status = "Draft"
	}
	id := newProcurementID("CNT")

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO contracts (id, vendor_id, title, start_date, end_date, value, currency, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, vendorID, title, startDate, endDate, value, currency, status,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("vendor=%s value=%.2f %s", vendorID, value, currency),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := models.Contract{
		ID: id, VendorID: vendorID, Title: title, Value: value, Currency: currency, Status: status,
	}
	if startDate != nil {
		out.StartDate = startDate.UTC().Format("2006-01-02")
	}
	if endDate != nil {
		out.EndDate = endDate.UTC().Format("2006-01-02")
	}
	return &out, nil
}
