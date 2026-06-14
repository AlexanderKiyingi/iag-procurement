package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

// CreateRfqQuote records a buyer-entered vendor quote against an open RFQ.
func (p *Procurement) CreateRfqQuote(ctx context.Context, rfqID, vendorID string, amount float64, currency, notes, auditUser string) (*models.RfqQuote, error) {
	rfqID = strings.TrimSpace(rfqID)
	vendorID = strings.TrimSpace(vendorID)
	if rfqID == "" || vendorID == "" {
		return nil, fmt.Errorf("%w: rfqId and vendorId are required", ErrInvalidArgument)
	}
	if amount < 0 {
		return nil, fmt.Errorf("%w: amount cannot be negative", ErrInvalidArgument)
	}
	if currency == "" {
		currency = "USD"
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var rfqStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM rfqs WHERE id = $1`, rfqID).Scan(&rfqStatus); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if s := strings.ToLower(strings.TrimSpace(rfqStatus)); s == "awarded" || s == "closed" {
		return nil, fmt.Errorf("%w: RFQ %s is %s; quotes can only be added while open", ErrInvalidArgument, rfqID, rfqStatus)
	}

	id := newProcurementID("QT")
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		INSERT INTO rfq_quotes (id, rfq_id, vendor_id, amount, currency, notes, created_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, rfqID, vendorID, amount, currency, strings.TrimSpace(notes), now, auditUser,
	); err != nil {
		return nil, err
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "create", id, fmt.Sprintf("rfq=%s vendor=%s amount=%.2f %s", rfqID, vendorID, amount, currency),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.RfqQuote{
		ID: id, RfqID: rfqID, VendorID: vendorID, Amount: amount, Currency: currency,
		Notes: strings.TrimSpace(notes), CreatedAt: now.Format(time.RFC3339), CreatedBy: auditUser,
	}, nil
}

// ListRfqQuotes returns all quotes recorded against an RFQ, newest first.
func (p *Procurement) ListRfqQuotes(ctx context.Context, rfqID string) ([]models.RfqQuote, error) {
	rfqID = strings.TrimSpace(rfqID)
	if rfqID == "" {
		return nil, fmt.Errorf("%w: rfqId is required", ErrInvalidArgument)
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id, rfq_id, vendor_id, amount, currency, notes, created_at, created_by
		FROM rfq_quotes WHERE rfq_id = $1 ORDER BY created_at DESC, id DESC`, rfqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.RfqQuote{}
	for rows.Next() {
		var q models.RfqQuote
		var created time.Time
		if err := rows.Scan(&q.ID, &q.RfqID, &q.VendorID, &q.Amount, &q.Currency, &q.Notes, &created, &q.CreatedBy); err != nil {
			return nil, err
		}
		q.CreatedAt = created.Format(time.RFC3339)
		out = append(out, q)
	}
	return out, rows.Err()
}

// AwardRfq awards a winning quote and converts it into a draft purchase order.
// The winner is identified by quoteID, or by vendorID (the vendor's latest
// quote). The PO inherits the quote amount/currency, the RFQ's source
// requisition, and a budget (explicit budgetID, else the requisition's budget),
// and takes its initial status from the approval threshold. The RFQ is marked
// Awarded with its winner recorded. All in one transaction.
func (p *Procurement) AwardRfq(ctx context.Context, rfqID, quoteID, vendorID, budgetID string, expectedDate *time.Time, auditUser string) (*models.Po, error) {
	rfqID = strings.TrimSpace(rfqID)
	if rfqID == "" {
		return nil, fmt.Errorf("%w: rfqId is required", ErrInvalidArgument)
	}
	quoteID = strings.TrimSpace(quoteID)
	vendorID = strings.TrimSpace(vendorID)
	if quoteID == "" && vendorID == "" {
		return nil, fmt.Errorf("%w: quoteId or vendorId is required to award", ErrInvalidArgument)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var rfqTitle, rfqStatus string
	var reqID *string
	if err := tx.QueryRow(ctx, `SELECT title, status, requisition_id FROM rfqs WHERE id = $1`, rfqID).
		Scan(&rfqTitle, &rfqStatus, &reqID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if s := strings.ToLower(strings.TrimSpace(rfqStatus)); s == "awarded" || s == "closed" {
		return nil, fmt.Errorf("%w: RFQ %s is already %s", ErrInvalidArgument, rfqID, rfqStatus)
	}

	// Resolve the winning quote.
	var (
		qVendor   string
		qAmount   float64
		qCurrency string
	)
	if quoteID != "" {
		err = tx.QueryRow(ctx, `
			SELECT vendor_id, amount, currency FROM rfq_quotes WHERE id = $1 AND rfq_id = $2`,
			quoteID, rfqID).Scan(&qVendor, &qAmount, &qCurrency)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT vendor_id, amount, currency FROM rfq_quotes
			WHERE rfq_id = $1 AND vendor_id = $2
			ORDER BY created_at DESC, id DESC LIMIT 1`, rfqID, vendorID).Scan(&qVendor, &qAmount, &qCurrency)
	}
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("%w: no matching quote to award for RFQ %s", ErrNotFound, rfqID)
	}
	if err != nil {
		return nil, err
	}

	// Budget: explicit wins, else inherit the source requisition's budget.
	budgetID = strings.TrimSpace(budgetID)
	if budgetID == "" && reqID != nil {
		var reqBudget *string
		if err := tx.QueryRow(ctx, `SELECT budget_id FROM requisitions WHERE id = $1`, *reqID).Scan(&reqBudget); err != nil && err != pgx.ErrNoRows {
			return nil, err
		} else if reqBudget != nil {
			budgetID = strings.TrimSpace(*reqBudget)
		}
	}

	poID := newProcurementID("PO-2026")
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	status := p.poInitialStatus(qAmount)
	title := strings.TrimSpace(rfqTitle)
	if title == "" {
		title = "Awarded from " + rfqID
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO purchase_orders (id, vendor_id, title, total, currency, status, created_at, expected_date, budget_id, requisition_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11)`,
		poID, qVendor, title, qAmount, qCurrency, status, createdDay, expectedDate, budgetID, reqID, auditUser,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE vendors SET open_pos = open_pos + 1 WHERE id = $1`, qVendor); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE rfqs SET winner_vendor_id = $2, status = 'Awarded', awarded_at = $3 WHERE id = $1`,
		rfqID, qVendor, now); err != nil {
		return nil, err
	}
	// Advance the source requisition to Ordered, mirroring CreatePurchaseOrder.
	if reqID != nil && strings.TrimSpace(*reqID) != "" {
		if _, err := tx.Exec(ctx, `UPDATE requisitions SET status = 'Ordered' WHERE id = $1`, *reqID); err != nil {
			return nil, err
		}
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
		auditUser, "award", rfqID, fmt.Sprintf("winner=%s po=%s amount=%.2f %s", qVendor, poID, qAmount, qCurrency),
	); err != nil {
		return nil, err
	}
	// Stage 2: book the firm encumbrance for the awarded PO (liquidating the
	// source requisition's pre-encumbrance when present).
	if err := p.applyPOEncumbrance(ctx, tx, poID, budgetID, deref(reqID), qAmount); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	out := models.Po{
		ID: poID, VendorID: qVendor, Title: title, Total: qAmount, Currency: qCurrency,
		Status: status, CreatedAt: createdDay.Format("2006-01-02"), BudgetID: budgetID, Items: []models.PoLine{},
	}
	if expectedDate != nil {
		out.ExpectedDate = expectedDate.UTC().Format("2006-01-02")
	}
	return &out, nil
}
