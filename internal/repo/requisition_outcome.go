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

// applyBudgetCommitment pre-encumbers (on approval) or releases (on rejection)
// the requisition total against its budget exactly once — stage 1 of the
// pre_committed -> committed -> spent lifecycle, tracked by
// requisitions.budget_committed ("pre-encumbered"). Runs inside the caller's
// transaction so the reservation and the status change are atomic. No-op without
// a budget or total. A rejection releases the pre-encumbrance only when it has
// not already been liquidated by a PO (pre_released), since the firm encumbrance
// then lives on the PO instead.
func (p *Procurement) applyBudgetCommitment(ctx context.Context, tx pgx.Tx, reqID, outcome string, alreadyCommitted, preReleased bool, budgetID string, total float64) error {
	if strings.TrimSpace(budgetID) == "" || total <= 0 {
		return nil
	}
	switch outcome {
	case "approved":
		if alreadyCommitted {
			return nil
		}
		// Lock the envelope and refuse to approve when the requisition would
		// overcommit it. allocated - pre_committed - committed - spent is the
		// headroom across all three stages.
		var allocated, preCommitted, committed, spent float64
		err := tx.QueryRow(ctx, `
			SELECT allocated, pre_committed, committed, spent FROM budgets WHERE id = $1 FOR UPDATE`,
			budgetID,
		).Scan(&allocated, &preCommitted, &committed, &spent)
		if errors.Is(err, pgx.ErrNoRows) {
			// No matching envelope: nothing to encumber, so skip the guard.
			return nil
		}
		if err != nil {
			return err
		}
		available := allocated - preCommitted - committed - spent
		if total-available > 0.005 {
			return fmt.Errorf("%w: requisition total %.2f exceeds remaining budget %.2f for %s",
				ErrInvalidArgument, total, available, budgetID)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE budgets
			SET pre_committed = pre_committed + $2,
			    remaining = allocated - (pre_committed + $2) - committed - spent
			WHERE id = $1`, budgetID, total); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE requisitions SET budget_committed = TRUE WHERE id = $1`, reqID)
		return err
	case "rejected":
		if !alreadyCommitted || preReleased {
			// Never pre-encumbered, or already liquidated by a PO: nothing to
			// release here.
			if alreadyCommitted {
				_, err := tx.Exec(ctx, `UPDATE requisitions SET budget_committed = FALSE WHERE id = $1`, reqID)
				return err
			}
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE budgets
			SET pre_committed = GREATEST(pre_committed - $2, 0),
			    remaining = allocated - GREATEST(pre_committed - $2, 0) - committed - spent
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

// applyPOEncumbrance books the firm encumbrance for a freshly raised PO — stage
// 2 of the pre_committed -> committed -> spent lifecycle. It liquidates the
// source requisition's pre-encumbrance exactly once (the estimate converts to a
// firm commitment), then adds the PO total to committed under a hard-ceiling
// guard. Idempotent via purchase_orders.budget_committed. Lock order is
// requisition, then budget, matching the global order used elsewhere. No-op
// without a budget envelope or a positive total.
func (p *Procurement) applyPOEncumbrance(ctx context.Context, tx pgx.Tx, poID, budgetID, reqID string, poTotal float64) error {
	budgetID = strings.TrimSpace(budgetID)
	if budgetID == "" || poTotal <= 0 {
		return nil
	}

	// 1) Liquidate the source requisition's pre-encumbrance once. FOR UPDATE on
	// the requisition stops two sibling POs both seeing pre_released=false.
	if rid := strings.TrimSpace(reqID); rid != "" {
		var reqBudget string
		var reqTotal float64
		var reqPre, reqReleased bool
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(budget_id, ''), total, budget_committed, pre_released
			FROM requisitions WHERE id = $1 FOR UPDATE`, rid,
		).Scan(&reqBudget, &reqTotal, &reqPre, &reqReleased)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && reqPre && !reqReleased && strings.TrimSpace(reqBudget) != "" && reqTotal > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE budgets
				SET pre_committed = GREATEST(pre_committed - $2, 0),
				    remaining = allocated - GREATEST(pre_committed - $2, 0) - committed - spent
				WHERE id = $1`, reqBudget, reqTotal); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE requisitions SET pre_released = TRUE WHERE id = $1`, rid); err != nil {
				return err
			}
		}
	}

	// 2) Add the firm encumbrance with a hard-ceiling guard (committed + spent
	// may not exceed allocated; pre_committed is a soft reservation and does not
	// gate firm commitment).
	var allocated, committed, spent float64
	err := tx.QueryRow(ctx, `
		SELECT allocated, committed, spent FROM budgets WHERE id = $1 FOR UPDATE`, budgetID).
		Scan(&allocated, &committed, &spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if committed+poTotal+spent-allocated > 0.005 {
		return fmt.Errorf("%w: purchase order total %.2f would exceed budget %s (committed %.2f, spent %.2f, allocated %.2f)",
			ErrInvalidArgument, poTotal, budgetID, committed, spent, allocated)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE budgets
		SET committed = committed + $2,
		    remaining = allocated - pre_committed - (committed + $2) - spent
		WHERE id = $1`, budgetID, poTotal); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE purchase_orders SET budget_committed = TRUE WHERE id = $1`, poID)
	return err
}

// releasePOEncumbrance reverses the open (un-received) portion of a PO's firm
// encumbrance back to the budget — used on PO reject/cancel/delete. The already
// recognized spend (spent_recognized) stays booked. Idempotent: callers gate on
// budget_committed and flip it false. Returns nil when there is nothing to release.
func (p *Procurement) releasePOEncumbrance(ctx context.Context, tx pgx.Tx, poID, budgetID string, poTotal, spentRecognized float64) error {
	budgetID = strings.TrimSpace(budgetID)
	open := poTotal - spentRecognized
	if budgetID == "" || open <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE budgets
		SET committed = GREATEST(committed - $2, 0),
		    remaining = allocated - pre_committed - GREATEST(committed - $2, 0) - spent
		WHERE id = $1`, budgetID, open); err != nil {
		return err
	}
	return nil
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

// recognizePOSpendOnReceipt is stage 3: it converts a PO's firm encumbrance to
// actual spend proportionally as goods are received, and reverses that on
// un-post/delete. Runs in the GRN-write tx with PO-then-budget lock order.
//
//   - GRN becomes "Posted" (not yet recognized): recognize amt = min(received
//     value, PO open), where received value = sum(qty*unit_price) over grn_lines
//     (or the full PO remaining when the GRN has no lines, preserving legacy
//     behavior). committed -= amt; spent += amt; po.spent_recognized += amt;
//     store the amount on the GRN for later reversal.
//   - GRN leaves "Posted" while previously recognized (un-post/delete): reverse
//     the stored recognized_amount exactly.
//
// No-op without a PO/budget. Idempotent via grns.budget_recognized.
func (p *Procurement) recognizePOSpendOnReceipt(ctx context.Context, tx pgx.Tx, g *models.Grn) error {
	if g == nil {
		return nil
	}
	posted := strings.TrimSpace(g.Status) == "Posted"

	var alreadyRecognized bool
	var recognizedAmount float64
	if err := tx.QueryRow(ctx, `
		SELECT budget_recognized, recognized_amount FROM grns WHERE id = $1 FOR UPDATE`, g.ID,
	).Scan(&alreadyRecognized, &recognizedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	poID := ""
	if g.PoID != nil {
		poID = strings.TrimSpace(*g.PoID)
	}

	switch {
	case posted && !alreadyRecognized:
		if poID == "" {
			return nil // no PO to recognize against; re-evaluated if a PO is added later
		}
		var budgetID string
		var total, spentRecognized float64
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(budget_id, ''), total, spent_recognized
			FROM purchase_orders WHERE id = $1 FOR UPDATE`, poID,
		).Scan(&budgetID, &total, &spentRecognized)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		open := total - spentRecognized
		if open <= 0 {
			// Fully recognized already; mark this GRN so it isn't re-evaluated.
			_, err := tx.Exec(ctx, `UPDATE grns SET budget_recognized = TRUE, recognized_amount = 0 WHERE id = $1`, g.ID)
			return err
		}
		var receivedValue float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(qty * unit_price), 0) FROM grn_lines WHERE grn_id = $1`, g.ID,
		).Scan(&receivedValue); err != nil {
			return err
		}
		if receivedValue <= 0 {
			receivedValue = open // no lines: recognize the full remaining (legacy behavior)
		}
		amt := receivedValue
		if amt > open {
			amt = open
		}
		if strings.TrimSpace(budgetID) != "" && amt > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE budgets
				SET spent = spent + $2,
				    committed = GREATEST(committed - $2, 0),
				    remaining = allocated - pre_committed - GREATEST(committed - $2, 0) - (spent + $2)
				WHERE id = $1`, budgetID, amt); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET spent_recognized = spent_recognized + $2 WHERE id = $1`, poID, amt); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE grns SET budget_recognized = TRUE, recognized_amount = $2 WHERE id = $1`, g.ID, amt)
		return err

	case !posted && alreadyRecognized:
		amt := recognizedAmount
		if poID != "" && amt > 0 {
			var budgetID string
			err := tx.QueryRow(ctx, `SELECT COALESCE(budget_id, '') FROM purchase_orders WHERE id = $1 FOR UPDATE`, poID).Scan(&budgetID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				if strings.TrimSpace(budgetID) != "" {
					if _, err := tx.Exec(ctx, `
						UPDATE budgets
						SET spent = GREATEST(spent - $2, 0),
						    committed = committed + $2,
						    remaining = allocated - pre_committed - (committed + $2) - GREATEST(spent - $2, 0)
						WHERE id = $1`, budgetID, amt); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET spent_recognized = GREATEST(spent_recognized - $2, 0) WHERE id = $1`, poID, amt); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE grns SET budget_recognized = FALSE, recognized_amount = 0 WHERE id = $1`, g.ID)
		return err
	}
	return nil
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
	poRef := ""
	if inv.PoID != nil {
		poRef = strings.TrimSpace(*inv.PoID)
	}
	key, payload, err := events.BuildInvoiceReceived(docRef, inv.VendorID, fmt.Sprintf("%.2f", inv.Amount), currency, poRef, due)
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
	// Monetary value of the received lines, so finance can book the GR/IR accrual.
	var receivedValue float64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(qty * unit_price), 0) FROM grn_lines WHERE grn_id = $1`, g.ID,
	).Scan(&receivedValue); err != nil {
		return err
	}
	valueStr := ""
	if receivedValue > 0 {
		valueStr = fmt.Sprintf("%.2f", receivedValue)
	}
	key, payload, err := events.BuildGrnPosted(g.ID, poID, g.VendorID, g.ReceivedBy, valueStr, lines)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, events.TypeGrnPosted, key, payload)
}
