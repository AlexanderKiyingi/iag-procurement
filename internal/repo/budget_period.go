package repo

import (
	"context"
	"fmt"
	"strings"
)

// BudgetCloseFilter narrows which budgets a period-close affects. Zero value
// (all fields empty/false) closes every still-open budget.
type BudgetCloseFilter struct {
	Period   string // optional exact period label match (e.g. "FY2026")
	BudgetID string // optional single budget id
	DueOnly  bool   // only budgets whose period_end has passed (scheduler use)
}

// CloseBudgetPeriod closes the matching open budgets under a policy and returns
// the ids closed. Idempotent: budgets already closed (period_closed_at set) are
// skipped. Runs in one transaction.
//
//   - "lapse": release open encumbrances — pre_committed and committed go to 0,
//     remaining = allocated - spent. Open commitments do NOT carry into the next
//     period.
//   - "carry": balances are retained (commitments carry forward); only the
//     closure timestamp is stamped.
func (p *Procurement) CloseBudgetPeriod(ctx context.Context, policy string, f BudgetCloseFilter, auditUser string) ([]string, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy != "lapse" && policy != "carry" {
		return nil, fmt.Errorf("%w: policy must be 'lapse' or 'carry'", ErrInvalidArgument)
	}

	where := []string{"period_closed_at IS NULL"}
	args := []any{}
	if s := strings.TrimSpace(f.Period); s != "" {
		args = append(args, s)
		where = append(where, fmt.Sprintf("period = $%d", len(args)))
	}
	if s := strings.TrimSpace(f.BudgetID); s != "" {
		args = append(args, s)
		where = append(where, fmt.Sprintf("id = $%d", len(args)))
	}
	if f.DueOnly {
		where = append(where, "period_end IS NOT NULL AND period_end < CURRENT_DATE")
	}
	whereSQL := strings.Join(where, " AND ")

	var setSQL string
	switch policy {
	case "lapse":
		setSQL = "pre_committed = 0, committed = 0, remaining = allocated - spent, period_closed_at = NOW()"
	case "carry":
		setSQL = "period_closed_at = NOW()"
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		"UPDATE budgets SET "+setSQL+" WHERE "+whereSQL+" RETURNING id", args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if auditUser == "" {
		auditUser = "system"
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_entries (username, action, target, detail) VALUES ($1,$2,$3,$4)`,
			auditUser, "budget.period.close", id, "policy="+policy,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}
