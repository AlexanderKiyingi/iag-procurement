package repo

import (
	"context"
	"time"

	"iag-procurement/backend/internal/models"
)

// The list routes for budgets, RFQs, GRNs, contracts and payments had no
// database-backed path at all: every request fell through to loadCached(), which
// serves a Redis snapshot built by Seed.Load() — a single pass that reads every
// procurement table at once.
//
// Two problems came from that. The snapshot is shared, so one unreadable column
// in one table (items.attrs, before migration 030 was applied) took out ten
// unrelated endpoints. And a client that dutifully sent ?limit/&offset — as
// every adapter in the iag-procurement frontend does — was silently served the
// whole cached table anyway, because parsePage's `ok` was never consulted on
// these routes.
//
// These five queries mirror the columns Seed.Load() reads for the same models,
// so a paged response and a snapshot response describe the same row.

// ListBudgets returns a filtered, paged slice of budgets (q matches code, dept
// or period).
func (p *Procurement) ListBudgets(ctx context.Context, limit, offset int, q string) ([]models.Budget, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE code ILIKE " + sp + " OR dept ILIKE " + sp + " OR period ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, code, period, allocated, pre_committed, committed, spent, remaining, dept, period_end, period_closed_at
		FROM budgets `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Budget{}
	for rows.Next() {
		var b models.Budget
		var periodEnd, periodClosedAt *time.Time
		if err := rows.Scan(&b.ID, &b.Code, &b.Period, &b.Allocated, &b.PreCommitted, &b.Committed,
			&b.Spent, &b.Remaining, &b.Dept, &periodEnd, &periodClosedAt); err != nil {
			return nil, err
		}
		b.PeriodEnd = dayStr(periodEnd)
		if periodClosedAt != nil {
			b.PeriodClosedAt = periodClosedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListRfqs returns a filtered, paged slice of RFQs (q matches id, title or
// status).
func (p *Procurement) ListRfqs(ctx context.Context, limit, offset int, q string) ([]models.Rfq, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE id ILIKE " + sp + " OR title ILIKE " + sp + " OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, title, status, due_date, created_at, winner_vendor_id, invited_vendor_ids
		FROM rfqs `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Rfq{}
	for rows.Next() {
		var r models.Rfq
		var due, created *time.Time
		var winner *string
		if err := rows.Scan(&r.ID, &r.Title, &r.Status, &due, &created, &winner, &r.InvitedVendors); err != nil {
			return nil, err
		}
		r.DueDate = dayStr(due)
		r.CreatedAt = dayStr(created)
		r.WinnerVendor = winner
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListGrns returns a filtered, paged slice of goods receipts with their lines
// (q matches id, vendor, received-by or status).
//
// Lines are fetched for the page in one follow-up query rather than per row —
// the same shape ListPurchaseOrders uses.
func (p *Procurement) ListGrns(ctx context.Context, limit, offset int, q string) ([]models.Grn, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE id ILIKE " + sp + " OR vendor_id ILIKE " + sp + " OR received_by ILIKE " + sp +
			" OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, po_id, vendor_id, received_date, received_by, status
		FROM grns `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Grn{}
	ids := []string{}
	for rows.Next() {
		var g models.Grn
		var poID *string
		var rd *time.Time
		if err := rows.Scan(&g.ID, &poID, &g.VendorID, &rd, &g.ReceivedBy, &g.Status); err != nil {
			return nil, err
		}
		g.PoID = poID
		g.ReceivedDate = dayStr(rd)
		g.Lines = []models.GrnLine{}
		out = append(out, g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}

	lineRows, err := p.pool.Query(ctx, `
		SELECT grn_id, item_id, qty, unit_price FROM grn_lines WHERE grn_id = ANY($1) ORDER BY grn_id, id`, ids)
	if err != nil {
		return nil, err
	}
	defer lineRows.Close()
	byGRN := map[string][]models.GrnLine{}
	for lineRows.Next() {
		var grnID string
		var ln models.GrnLine
		if err := lineRows.Scan(&grnID, &ln.ItemID, &ln.Qty, &ln.UnitPrice); err != nil {
			return nil, err
		}
		byGRN[grnID] = append(byGRN[grnID], ln)
	}
	if err := lineRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if lines, ok := byGRN[out[i].ID]; ok {
			out[i].Lines = lines
		}
	}
	return out, nil
}

// ListContracts returns a filtered, paged slice of contracts (q matches id,
// title, vendor or status).
func (p *Procurement) ListContracts(ctx context.Context, limit, offset int, q string) ([]models.Contract, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE id ILIKE " + sp + " OR title ILIKE " + sp + " OR vendor_id ILIKE " + sp +
			" OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, vendor_id, title, start_date, end_date, value, currency, status
		FROM contracts `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Contract{}
	for rows.Next() {
		var ct models.Contract
		var sd, ed *time.Time
		if err := rows.Scan(&ct.ID, &ct.VendorID, &ct.Title, &sd, &ed, &ct.Value, &ct.Currency, &ct.Status); err != nil {
			return nil, err
		}
		ct.StartDate = dayStr(sd)
		ct.EndDate = dayStr(ed)
		out = append(out, ct)
	}
	return out, rows.Err()
}

// ListPayments returns a filtered, paged slice of payments (q matches id,
// reference, vendor, invoice or status).
func (p *Procurement) ListPayments(ctx context.Context, limit, offset int, q string) ([]models.Payment, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE id ILIKE " + sp + " OR COALESCE(reference,'') ILIKE " + sp + " OR vendor_id ILIKE " + sp +
			" OR COALESCE(invoice_id,'') ILIKE " + sp + " OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, invoice_id, vendor_id, amount, currency, pay_date, method, reference, status, initiated_by
		FROM payments `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Payment{}
	for rows.Next() {
		var pm models.Payment
		var pd *time.Time
		if err := rows.Scan(&pm.ID, &pm.InvoiceID, &pm.VendorID, &pm.Amount, &pm.Currency, &pd,
			&pm.Method, &pm.Reference, &pm.Status, &pm.InitiatedBy); err != nil {
			return nil, err
		}
		pm.Date = dayStr(pd)
		out = append(out, pm)
	}
	return out, rows.Err()
}

// ListAudit returns a filtered, paged slice of audit entries, newest first
// (q matches username, action, target or detail).
//
// Ordered by id DESC rather than ascending like the collections above: an audit
// trail is read from the most recent entry, so page one has to be the newest
// rows. The cached fallback returns them oldest-first, which meant the first
// page a reader saw was the oldest history in the system.
func (p *Procurement) ListAudit(ctx context.Context, limit, offset int, q string) ([]models.AuditEntry, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE username ILIKE " + sp + " OR action ILIKE " + sp + " OR target ILIKE " + sp +
			" OR detail ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, ts, username, action, target, detail
		FROM audit_entries `+where+`ORDER BY id DESC LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AuditEntry{}
	for rows.Next() {
		var e models.AuditEntry
		var ts *time.Time
		if err := rows.Scan(&e.ID, &ts, &e.User, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		if ts != nil {
			e.Timestamp = ts.UTC().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
