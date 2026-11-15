package repo

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/events"
)

// enqueueBudgetCommitmentSnapshot publishes the envelope's current balances so
// finance can refresh its commitment read model. Soft-check / hard ceiling stay
// local to procurement; this event is visibility only.
func (p *Procurement) enqueueBudgetCommitmentSnapshot(ctx context.Context, tx pgx.Tx, budgetID string) error {
	if p.outbox == nil {
		return nil
	}
	budgetID = strings.TrimSpace(budgetID)
	if budgetID == "" {
		return nil
	}
	var code, period, dept string
	var allocated, pre, committed, spent, remaining float64
	err := tx.QueryRow(ctx, `
		SELECT code, period, COALESCE(dept, ''), allocated, pre_committed, committed, spent, remaining
		FROM budgets WHERE id = $1`, budgetID,
	).Scan(&code, &period, &dept, &allocated, &pre, &committed, &spent, &remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key, payload, err := events.BuildBudgetCommitmentChanged(
		budgetID, code, period, dept, allocated, pre, committed, spent, remaining,
	)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, events.TypeBudgetCommitmentChanged, key, payload)
}
