package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"iag-procurement/backend/internal/models"
)

type Seed struct {
	pool *pgxpool.Pool
}

func NewSeed(pool *pgxpool.Pool) *Seed {
	return &Seed{pool: pool}
}

func fdate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func fatime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04")
}

func (s *Seed) Load(ctx context.Context) (*models.SeedData, error) {
	out := &models.SeedData{}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, name, logo, category, contact, email, phone, country, terms, rating, status, total_spend, open_pos
		FROM vendors ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("vendors: %w", err)
		}
		for rows.Next() {
			var v models.Vendor
			if err := rows.Scan(&v.ID, &v.Name, &v.Logo, &v.Category, &v.Contact, &v.Email, &v.Phone, &v.Country, &v.Terms, &v.Rating, &v.Status, &v.TotalSpend, &v.OpenPOs); err != nil {
				rows.Close()
				return nil, err
			}
			out.Vendors = append(out.Vendors, v)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, sku, name, category, uom, stock, reorder, last_price, currency, preferred_vendor_id
		FROM items ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		for rows.Next() {
			var it models.Item
			var pref *string
			if err := rows.Scan(&it.ID, &it.SKU, &it.Name, &it.Category, &it.UOM, &it.Stock, &it.Reorder, &it.LastPrice, &it.Currency, &pref); err != nil {
				rows.Close()
				return nil, err
			}
			if pref != nil {
				it.PreferredVendor = *pref
			}
			out.Items = append(out.Items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, code, period, allocated, committed, spent, remaining, dept FROM budgets ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("budgets: %w", err)
		}
		for rows.Next() {
			var b models.Budget
			if err := rows.Scan(&b.ID, &b.Code, &b.Period, &b.Allocated, &b.Committed, &b.Spent, &b.Remaining, &b.Dept); err != nil {
				rows.Close()
				return nil, err
			}
			out.Budgets = append(out.Budgets, b)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id
		FROM requisitions ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("requisitions: %w", err)
		}
		for rows.Next() {
			var r models.Requisition
			var ca, nb *time.Time
			if err := rows.Scan(&r.ID, &r.Title, &r.Dept, &r.Requester, &r.Priority, &r.Status, &ca, &nb, &r.Total, &r.Currency, &r.BudgetID); err != nil {
				rows.Close()
				return nil, err
			}
			r.CreatedAt = fdate(ca)
			r.NeededBy = fdate(nb)
			out.Requisitions = append(out.Requisitions, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, title, status, due_date, created_at, winner_vendor_id, invited_vendor_ids
		FROM rfqs ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("rfqs: %w", err)
		}
		for rows.Next() {
			var q models.Rfq
			var due, created *time.Time
			var winner *string
			if err := rows.Scan(&q.ID, &q.Title, &q.Status, &due, &created, &winner, &q.InvitedVendors); err != nil {
				rows.Close()
				return nil, err
			}
			q.DueDate = fdate(due)
			q.CreatedAt = fdate(created)
			q.WinnerVendor = winner
			out.Rfqs = append(out.Rfqs, q)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	linesByPO := map[string][]models.PoLine{}
	{
		rows, err := s.pool.Query(ctx, `SELECT po_id, item_id, qty, unit_price FROM po_lines ORDER BY po_id, id`)
		if err != nil {
			return nil, fmt.Errorf("po_lines: %w", err)
		}
		for rows.Next() {
			var poID, itemID string
			var qty, price float64
			if err := rows.Scan(&poID, &itemID, &qty, &price); err != nil {
				rows.Close()
				return nil, err
			}
			linesByPO[poID] = append(linesByPO[poID], models.PoLine{ItemID: itemID, Qty: qty, Price: price})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, vendor_id, title, total, currency, status, created_at, expected_date, budget_id
		FROM purchase_orders ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("purchase_orders: %w", err)
		}
		for rows.Next() {
			var p models.Po
			var ca, ex *time.Time
			if err := rows.Scan(&p.ID, &p.VendorID, &p.Title, &p.Total, &p.Currency, &p.Status, &ca, &ex, &p.BudgetID); err != nil {
				rows.Close()
				return nil, err
			}
			p.CreatedAt = fdate(ca)
			p.ExpectedDate = fdate(ex)
			p.Items = linesByPO[p.ID]
			if p.Items == nil {
				p.Items = []models.PoLine{}
			}
			out.Pos = append(out.Pos, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, po_id, vendor_id, received_date, received_by, status FROM grns ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("grns: %w", err)
		}
		for rows.Next() {
			var g models.Grn
			var poID *string
			var rd *time.Time
			if err := rows.Scan(&g.ID, &poID, &g.VendorID, &rd, &g.ReceivedBy, &g.Status); err != nil {
				rows.Close()
				return nil, err
			}
			g.PoID = poID
			g.ReceivedDate = fdate(rd)
			out.Grns = append(out.Grns, g)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, invoice_no, vendor_id, po_id, amount, currency, status, match_status, invoice_date
		FROM invoices ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("invoices: %w", err)
		}
		for rows.Next() {
			var inv models.Invoice
			var invNo *string
			var poID *string
			var idate *time.Time
			if err := rows.Scan(&inv.ID, &invNo, &inv.VendorID, &poID, &inv.Amount, &inv.Currency, &inv.Status, &inv.MatchStatus, &idate); err != nil {
				rows.Close()
				return nil, err
			}
			inv.InvoiceNo = invNo
			inv.PoID = poID
			inv.InvoiceDate = fdate(idate)
			out.Invoices = append(out.Invoices, inv)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, vendor_id, title, start_date, end_date, value, currency, status FROM contracts ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("contracts: %w", err)
		}
		for rows.Next() {
			var ct models.Contract
			var sd, ed *time.Time
			if err := rows.Scan(&ct.ID, &ct.VendorID, &ct.Title, &sd, &ed, &ct.Value, &ct.Currency, &ct.Status); err != nil {
				rows.Close()
				return nil, err
			}
			ct.StartDate = fdate(sd)
			ct.EndDate = fdate(ed)
			out.Contracts = append(out.Contracts, ct)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, invoice_id, vendor_id, amount, currency, pay_date, method, reference, status, initiated_by
		FROM payments ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("payments: %w", err)
		}
		for rows.Next() {
			var p models.Payment
			var pd *time.Time
			if err := rows.Scan(&p.ID, &p.InvoiceID, &p.VendorID, &p.Amount, &p.Currency, &pd, &p.Method, &p.Reference, &p.Status, &p.InitiatedBy); err != nil {
				rows.Close()
				return nil, err
			}
			p.Date = fdate(pd)
			out.Payments = append(out.Payments, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	{
		rows, err := s.pool.Query(ctx, `
		SELECT id, ts, username, action, target, detail FROM audit_entries ORDER BY id`)
		if err != nil {
			return nil, fmt.Errorf("audit: %w", err)
		}
		for rows.Next() {
			var a models.AuditEntry
			var ts time.Time
			if err := rows.Scan(&a.ID, &ts, &a.User, &a.Action, &a.Target, &a.Detail); err != nil {
				rows.Close()
				return nil, err
			}
			a.Timestamp = fatime(ts)
			out.Audit = append(out.Audit, a)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	return out, nil
}
