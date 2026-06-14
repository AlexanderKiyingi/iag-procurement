package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/models"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sameActor reports whether two actor identities refer to the same person, used
// for segregation-of-duties checks. Blank/"unknown" actors never match (we
// can't prove self-approval, so we don't block it).
func sameActor(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" || strings.EqualFold(a, "unknown") {
		return false
	}
	return strings.EqualFold(a, b)
}

// applyBudgetCommitment encumbers (on approval) or releases (on rejection) the
// requisition total against its budget exactly once, tracked by
// requisitions.budget_committed. Runs inside the caller's transaction so the
// commitment and the status change are atomic. No-op without a budget or total.
func (p *Procurement) applyBudgetCommitment(ctx context.Context, tx pgx.Tx, reqID, outcome string, alreadyCommitted bool, budgetID string, total float64) error {
	if strings.TrimSpace(budgetID) == "" || total <= 0 {
		return nil
	}
	switch outcome {
	case "approved":
		if alreadyCommitted {
			return nil
		}
		// Lock the envelope and refuse to approve when the requisition would
		// overcommit it. allocated - spent - committed is the headroom; a
		// negative result here means the approval was previously unguarded.
		var allocated, spent, committed float64
		err := tx.QueryRow(ctx, `
			SELECT allocated, spent, committed FROM budgets WHERE id = $1 FOR UPDATE`,
			budgetID,
		).Scan(&allocated, &spent, &committed)
		if errors.Is(err, pgx.ErrNoRows) {
			// No matching envelope: nothing to encumber, so skip the guard.
			return nil
		}
		if err != nil {
			return err
		}
		available := allocated - spent - committed
		if total-available > 0.005 {
			return fmt.Errorf("%w: requisition total %.2f exceeds remaining budget %.2f for %s",
				ErrInvalidArgument, total, available, budgetID)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE budgets
			SET committed = committed + $2,
			    remaining = allocated - spent - (committed + $2)
			WHERE id = $1`, budgetID, total); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE requisitions SET budget_committed = TRUE WHERE id = $1`, reqID)
		return err
	case "rejected":
		if !alreadyCommitted {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE budgets
			SET committed = GREATEST(committed - $2, 0),
			    remaining = allocated - spent - GREATEST(committed - $2, 0)
			WHERE id = $1`, budgetID, total); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE requisitions SET budget_committed = FALSE WHERE id = $1`, reqID)
		return err
	}
	return nil
}

// enqueueRequisitionOutcome writes the approval/rejection event into the outbox
// in the caller's transaction, so the cross-service notification commits
// atomically with the status change (or not at all). No-op when the outbox is
// not configured.
func (p *Procurement) enqueueRequisitionOutcome(ctx context.Context, tx pgx.Tx, outcome, reqID, pmReqID, pmOwner, actor, budgetID string) error {
	if p.outbox == nil {
		return nil
	}
	eventType := events.TypeRequisitionApproved
	if outcome == "rejected" {
		eventType = events.TypeRequisitionRejected
	}
	key, payload, err := events.BuildRequisitionOutcome(eventType, reqID, pmReqID, pmOwner, actor, budgetID)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, eventType, key, payload)
}

// poPreApprovalStatuses are the PO states from which goods may not yet be
// received (the PO has not cleared approval).
var poPreApprovalStatuses = map[string]bool{
	"draft":            true,
	"pending approval": true,
	"rejected":         true,
	"cancelled":        true,
}

// assertPOReceivable rejects a goods receipt against a PO that has not cleared
// approval, enforcing "only approved POs proceed downstream". A blank or unknown
// PO reference is left to the FK / three-way match to handle.
func (p *Procurement) assertPOReceivable(ctx context.Context, tx pgx.Tx, poID string) error {
	poID = strings.TrimSpace(poID)
	if poID == "" {
		return nil
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM purchase_orders WHERE id = $1`, poID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if poPreApprovalStatuses[strings.ToLower(strings.TrimSpace(status))] {
		return fmt.Errorf("%w: cannot receive against PO %s in status %q (must be approved)", ErrInvalidArgument, poID, status)
	}
	return nil
}

// recognizePOSpendOnReceipt converts a PO's budget encumbrance to actual spend
// when its first goods receipt is posted: committed -> spent by the PO total on
// the PO's budget, exactly once (tracked by purchase_orders.budget_spent), so
// repeated/partial GRNs don't double-count. Runs in the GRN-write tx. No-op
// unless the GRN is "Posted" and the PO has a budget and a positive total.
func (p *Procurement) recognizePOSpendOnReceipt(ctx context.Context, tx pgx.Tx, g *models.Grn) error {
	if g == nil || strings.TrimSpace(g.Status) != "Posted" {
		return nil
	}
	poID := ""
	if g.PoID != nil {
		poID = strings.TrimSpace(*g.PoID)
	}
	if poID == "" {
		return nil
	}
	var budgetID string
	var total float64
	var alreadySpent bool
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(budget_id, ''), total, budget_spent
		FROM purchase_orders WHERE id = $1 FOR UPDATE`, poID,
	).Scan(&budgetID, &total, &alreadySpent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if alreadySpent || strings.TrimSpace(budgetID) == "" || total <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE budgets
		SET spent = spent + $2,
		    committed = GREATEST(committed - $2, 0),
		    remaining = allocated - (spent + $2) - GREATEST(committed - $2, 0)
		WHERE id = $1`, budgetID, total); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE purchase_orders SET budget_spent = TRUE WHERE id = $1`, poID)
	return err
}

// enqueueInvoiceReceived writes procurement.invoice.received into the outbox in
// the invoice-insert transaction, so iag-finance's AP item commits atomically
// with the invoice (or not at all). No-op when the outbox is not configured.
func (p *Procurement) enqueueInvoiceReceived(ctx context.Context, tx pgx.Tx, inv *models.Invoice) error {
	if p.outbox == nil || inv == nil {
		return nil
	}
	docRef := inv.ID
	if inv.InvoiceNo != nil && strings.TrimSpace(*inv.InvoiceNo) != "" {
		docRef = strings.TrimSpace(*inv.InvoiceNo)
	}
	currency := inv.Currency
	if currency == "" {
		currency = "UGX"
	}
	var due *time.Time
	if inv.InvoiceDate != "" {
		if t, err := time.Parse("2006-01-02", inv.InvoiceDate); err == nil {
			due = &t
		}
	}
	key, payload, err := events.BuildInvoiceReceived(docRef, inv.VendorID, fmt.Sprintf("%.2f", inv.Amount), currency, due)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, events.TypeInvoiceReceived, key, payload)
}

// enqueueGrnPosted writes procurement.grn.posted into the outbox in the GRN
// write transaction when the receipt reaches "Posted", carrying the PO lines so
// iag-warehouse can draft an intake. No-op when the outbox is not configured or
// the GRN is not yet posted.
func (p *Procurement) enqueueGrnPosted(ctx context.Context, tx pgx.Tx, g *models.Grn) error {
	if p.outbox == nil || g == nil || strings.TrimSpace(g.Status) != "Posted" {
		return nil
	}
	poID := ""
	if g.PoID != nil {
		poID = strings.TrimSpace(*g.PoID)
	}
	var lines []events.GrnPostedLine
	if poID != "" {
		rows, err := tx.Query(ctx, `
			SELECT i.sku, pl.qty, i.uom
			FROM po_lines pl
			JOIN items i ON i.id = pl.item_id
			WHERE pl.po_id = $1
			ORDER BY pl.id`, poID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var l events.GrnPostedLine
			if err := rows.Scan(&l.SKU, &l.Qty, &l.UOM); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		err = rows.Err()
		rows.Close() // must close before the next query on this tx connection
		if err != nil {
			return err
		}
	}
	key, payload, err := events.BuildGrnPosted(g.ID, poID, g.VendorID, g.ReceivedBy, lines)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, events.TypeGrnPosted, key, payload)
}
