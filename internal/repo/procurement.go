package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"iag-procurement/backend/internal/models"
	"iag-procurement/backend/internal/outbox"
)

type Procurement struct {
	pool              *pgxpool.Pool
	outbox            *outbox.Store
	approvalThreshold float64
}

// SetOutbox wires the transactional outbox so requisition approval/rejection
// events are enqueued atomically with the status change and drained to Kafka by
// a background publisher. Nil leaves outbound events un-emitted (e.g. tests).
func (p *Procurement) SetOutbox(store *outbox.Store) { p.outbox = store }

// SetApprovalThreshold sets the PO total at/above which a purchase order is
// created "Pending Approval" rather than auto-"Approved". 0 means every PO
// requires approval (see config.ApprovalThreshold).
func (p *Procurement) SetApprovalThreshold(v float64) { p.approvalThreshold = v }

// poInitialStatus returns the status a newly created PO of the given total
// should take under the configured approval threshold.
func (p *Procurement) poInitialStatus(total float64) string {
	if p.approvalThreshold > 0 && total < p.approvalThreshold {
		return "Approved"
	}
	return "Pending Approval"
}

func NewProcurement(pool *pgxpool.Pool) *Procurement {
	return &Procurement{pool: pool}
}

func newProcurementID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b[:]))
}

// CreateRequisition inserts a requisition and an audit trail row.
func (p *Procurement) CreateRequisition(ctx context.Context, title, dept, requester, priority, status string, neededBy *time.Time, total float64, currency, budgetID, auditUser string) (*models.Requisition, error) {
	if currency == "" {
		currency = "USD"
	}
	if priority == "" {
		priority = "Medium"
	}
	if status == "" {
		status = "Pending Approval"
	}
	id := newProcurementID("PR-2026")
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO requisitions (id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, title, dept, requester, priority, status, createdDay, neededBy, total, currency, budgetID,
	); err != nil {
		return nil, err
	}

	detail := fmt.Sprintf("budget=%s total=%.2f %s", budgetID, total, currency)
	if auditUser == "" {
		auditUser = "unknown"
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

	out := models.Requisition{
		ID:        id,
		Title:     title,
		Dept:      dept,
		Requester: requester,
		Priority:  priority,
		Status:    status,
		CreatedAt: createdDay.Format("2006-01-02"),
		Total:     total,
		Currency:  currency,
		BudgetID:  budgetID,
	}
	if neededBy != nil {
		out.NeededBy = neededBy.UTC().Format("2006-01-02")
	}
	return &out, nil
}

// CreatePurchaseOrder inserts a PO, line items, vendor open_po bump, and audit row.
func (p *Procurement) CreatePurchaseOrder(ctx context.Context, vendorID, title, currency, budgetID, requisitionID string, expectedDate *time.Time, lines []models.PoLine, auditUser string) (*models.Po, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("at least one line item is required")
	}
	if currency == "" {
		currency = "USD"
	}
	var total float64
	for _, ln := range lines {
		if ln.Qty <= 0 || ln.Price < 0 {
			return nil, fmt.Errorf("invalid line qty/price")
		}
		total += ln.Qty * ln.Price
	}

	id := newProcurementID("PO-2026")
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	status := p.poInitialStatus(total)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO purchase_orders (id, vendor_id, title, total, currency, status, created_at, expected_date, budget_id, requisition_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11)`,
		id, vendorID, title, total, currency, status, createdDay, expectedDate, budgetID, strings.TrimSpace(requisitionID), auditUser,
	); err != nil {
		return nil, err
	}

	// Link the PO back to its source requisition and advance the requisition
	// to "Ordered" so its lifecycle reflects fulfillment (best-effort: only
	// when a real requisition id was supplied).
	if rid := strings.TrimSpace(requisitionID); rid != "" {
		if _, err := tx.Exec(ctx, `UPDATE requisitions SET status = 'Ordered' WHERE id = $1`, rid); err != nil {
			return nil, err
		}
	}

	for _, ln := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO po_lines (po_id, item_id, qty, unit_price) VALUES ($1,$2,$3,$4)`,
			id, ln.ItemID, ln.Qty, ln.Price,
		); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE vendors SET open_pos = open_pos + 1 WHERE id = $1`, vendorID); err != nil {
		return nil, err
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	detail := fmt.Sprintf("vendor=%s lines=%d total=%.2f %s", vendorID, len(lines), total, currency)
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

	out := models.Po{
		ID:           id,
		VendorID:     vendorID,
		Title:        title,
		Total:        total,
		Currency:     currency,
		Status:       status,
		CreatedAt:    createdDay.Format("2006-01-02"),
		BudgetID:     budgetID,
		Items:        append([]models.PoLine(nil), lines...),
		ExpectedDate: "",
	}
	if expectedDate != nil {
		out.ExpectedDate = expectedDate.UTC().Format("2006-01-02")
	}
	return &out, nil
}
