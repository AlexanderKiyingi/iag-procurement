package repo

import (
	"context"
	"fmt"
	"time"

	"iag-procurement/backend/internal/models"
)

// pageArgs assembles the query args plus the placeholder tokens for an optional
// ILIKE search term ($1 when present) followed by LIMIT/OFFSET. Callers build
// the WHERE clause from searchParam and append ORDER BY ... LIMIT/OFFSET.
func pageArgs(q string, limit, offset int) (args []any, searchParam, limitParam, offsetParam string) {
	if q != "" {
		args = append(args, "%"+q+"%")
		searchParam = "$1"
	}
	args = append(args, limit, offset)
	limitParam = fmt.Sprintf("$%d", len(args)-1)
	offsetParam = fmt.Sprintf("$%d", len(args))
	return
}

func dayStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// ListVendors returns a filtered, paged slice of vendors (q matches name,
// category, country, or email).
func (p *Procurement) ListVendors(ctx context.Context, limit, offset int, q string) ([]models.Vendor, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE name ILIKE " + sp + " OR category ILIKE " + sp + " OR country ILIKE " + sp + " OR email ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, name, logo, category, contact, email, phone, country, terms, rating, status, total_spend, open_pos
		FROM vendors `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Vendor{}
	for rows.Next() {
		var v models.Vendor
		if err := rows.Scan(&v.ID, &v.Name, &v.Logo, &v.Category, &v.Contact, &v.Email, &v.Phone,
			&v.Country, &v.Terms, &v.Rating, &v.Status, &v.TotalSpend, &v.OpenPOs); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListItems returns a filtered, paged slice of catalog items (q matches sku,
// name, or category).
func (p *Procurement) ListItems(ctx context.Context, limit, offset int, q string) ([]models.Item, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE sku ILIKE " + sp + " OR name ILIKE " + sp + " OR category ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, sku, name, category, uom, stock, reorder, last_price, currency, COALESCE(preferred_vendor_id, '')
		FROM items `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Item{}
	for rows.Next() {
		var it models.Item
		if err := rows.Scan(&it.ID, &it.SKU, &it.Name, &it.Category, &it.UOM, &it.Stock, &it.Reorder,
			&it.LastPrice, &it.Currency, &it.PreferredVendor); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListRequisitions returns a filtered, paged slice of requisitions (q matches
// title, dept, requester, or status).
func (p *Procurement) ListRequisitions(ctx context.Context, limit, offset int, q string) ([]models.Requisition, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE title ILIKE " + sp + " OR dept ILIKE " + sp + " OR requester ILIKE " + sp + " OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id
		FROM requisitions `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Requisition{}
	for rows.Next() {
		var r models.Requisition
		var created, needed *time.Time
		if err := rows.Scan(&r.ID, &r.Title, &r.Dept, &r.Requester, &r.Priority, &r.Status,
			&created, &needed, &r.Total, &r.Currency, &r.BudgetID); err != nil {
			return nil, err
		}
		r.CreatedAt = dayStr(created)
		r.NeededBy = dayStr(needed)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPurchaseOrders returns a filtered, paged slice of POs with their lines
// (q matches id, title, vendor, or status).
func (p *Procurement) ListPurchaseOrders(ctx context.Context, limit, offset int, q string) ([]models.Po, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE id ILIKE " + sp + " OR title ILIKE " + sp + " OR vendor_id ILIKE " + sp + " OR status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, vendor_id, title, total, currency, status, created_at, expected_date, COALESCE(budget_id, '')
		FROM purchase_orders `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Po{}
	ids := []string{}
	for rows.Next() {
		var po models.Po
		var created, expected *time.Time
		if err := rows.Scan(&po.ID, &po.VendorID, &po.Title, &po.Total, &po.Currency, &po.Status,
			&created, &expected, &po.BudgetID); err != nil {
			return nil, err
		}
		po.CreatedAt = dayStr(created)
		po.ExpectedDate = dayStr(expected)
		po.Items = []models.PoLine{}
		out = append(out, po)
		ids = append(ids, po.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}

	// Attach lines for the page in one query.
	lineRows, err := p.pool.Query(ctx, `
		SELECT po_id, item_id, qty, unit_price FROM po_lines WHERE po_id = ANY($1) ORDER BY po_id, id`, ids)
	if err != nil {
		return nil, err
	}
	defer lineRows.Close()
	byPO := map[string][]models.PoLine{}
	for lineRows.Next() {
		var poID string
		var ln models.PoLine
		if err := lineRows.Scan(&poID, &ln.ItemID, &ln.Qty, &ln.Price); err != nil {
			return nil, err
		}
		byPO[poID] = append(byPO[poID], ln)
	}
	if err := lineRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if lines, ok := byPO[out[i].ID]; ok {
			out[i].Items = lines
		}
	}
	return out, nil
}

// ListInvoices returns a filtered, paged slice of invoices (q matches invoice
// no, id, vendor, status, or match status).
func (p *Procurement) ListInvoices(ctx context.Context, limit, offset int, q string) ([]models.Invoice, error) {
	args, sp, lp, op := pageArgs(q, limit, offset)
	where := ""
	if sp != "" {
		where = "WHERE COALESCE(invoice_no,'') ILIKE " + sp + " OR id ILIKE " + sp + " OR vendor_id ILIKE " + sp +
			" OR status ILIKE " + sp + " OR match_status ILIKE " + sp + " "
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, invoice_no, vendor_id, po_id, amount, currency, status, match_status, invoice_date
		FROM invoices `+where+`ORDER BY id LIMIT `+lp+` OFFSET `+op, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Invoice{}
	for rows.Next() {
		var inv models.Invoice
		var idate *time.Time
		if err := rows.Scan(&inv.ID, &inv.InvoiceNo, &inv.VendorID, &inv.PoID, &inv.Amount,
			&inv.Currency, &inv.Status, &inv.MatchStatus, &idate); err != nil {
			return nil, err
		}
		inv.InvoiceDate = dayStr(idate)
		out = append(out, inv)
	}
	return out, rows.Err()
}
