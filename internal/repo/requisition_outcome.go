package repo

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/events"
)

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
		if _, err := tx.Exec(ctx, `
			UPDATE budgets
			SET committed = committed + $2,
			    remaining = allocated - spent - (committed + $2)
			WHERE id = $1`, budgetID, total); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE requisitions SET budget_committed = TRUE WHERE id = $1`, reqID)
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
