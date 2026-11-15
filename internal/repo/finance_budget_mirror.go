package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

// UpsertBudgetFromFinance mirrors a finance.budget.upserted plan line into a
// procurement envelope. code must be the stable FIN:… key; dept is the
// canonical cost-centre code. Updates allocated and recomputes remaining
// without touching encumbrance balances.
func (p *Procurement) UpsertBudgetFromFinance(ctx context.Context, code, period string, allocated float64, dept string) (*models.Budget, error) {
	code = strings.TrimSpace(code)
	period = strings.TrimSpace(period)
	dept = strings.TrimSpace(dept)
	if code == "" || period == "" {
		return nil, fmt.Errorf("%w: code and period are required", ErrInvalidArgument)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var (
		id                    string
		pre, committed, spent float64
	)
	err = tx.QueryRow(ctx, `
		SELECT id::text, pre_committed, committed, spent
		FROM budgets WHERE code = $1
		FOR UPDATE`, code).Scan(&id, &pre, &committed, &spent)
	switch {
	case err == nil:
		remaining := allocated - pre - committed - spent
		if _, err := tx.Exec(ctx, `
			UPDATE budgets SET
				period = $2,
				allocated = $3,
				dept = $4,
				remaining = $5
			WHERE id = $1::uuid`,
			id, period, allocated, dept, remaining); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_entries (username, action, target, detail)
			VALUES ($1,$2,$3,$4)`,
			"system:iag.finance", "update", id,
			fmt.Sprintf("finance mirror allocated=%.2f dept=%s period=%s", allocated, dept, period),
		); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &models.Budget{
			ID: id, Code: code, Period: period, Allocated: allocated,
			PreCommitted: pre, Committed: committed, Spent: spent, Remaining: remaining, Dept: dept,
		}, nil
	case err != pgx.ErrNoRows:
		return nil, err
	}

	remaining := allocated
	if err := tx.QueryRow(ctx, `
		INSERT INTO budgets (code, period, allocated, committed, spent, remaining, dept)
		VALUES ($1,$2,$3,0,0,$4,$5) RETURNING id::text`,
		code, period, allocated, remaining, dept,
	).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		"system:iag.finance", "create", id,
		fmt.Sprintf("finance mirror allocated=%.2f dept=%s period=%s", allocated, dept, period),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.Budget{
		ID: id, Code: code, Period: period, Allocated: allocated,
		Remaining: remaining, Dept: dept,
	}, nil
}
