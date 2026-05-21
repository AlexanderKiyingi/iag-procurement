package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

var ErrPMRequisitionExists = errors.New("pm requisition already imported")

// ImportPMRequisition creates a procurement requisition from a PM workspace event (idempotent on pmID).
func (p *Procurement) ImportPMRequisition(
	ctx context.Context,
	pmID, title, dept, requester, priority, status string,
	total float64,
	currency, budgetID, sourceEventID string,
) (*models.Requisition, error) {
	pmID = strings.TrimSpace(pmID)
	if pmID == "" {
		return nil, fmt.Errorf("pm requisition id required")
	}
	var exists bool
	if err := p.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM requisitions WHERE pm_requisition_id = $1)`,
		pmID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrPMRequisitionExists
	}

	if currency == "" {
		currency = "USD"
	}
	if priority == "" {
		priority = "Medium"
	}
	if status == "" {
		status = "Pending Approval"
	}
	if budgetID == "" {
		budgetID = "BDG-2026-UT"
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
		INSERT INTO requisitions (id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id, pm_requisition_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10,$11)`,
		id, title, dept, requester, priority, status, createdDay, total, currency, budgetID, pmID,
	); err != nil {
		return nil, err
	}

	detail := fmt.Sprintf("pmId=%s event=%s total=%.2f %s budget=%s", pmID, sourceEventID, total, currency, budgetID)
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		"pm-integration", "pm.import", id, detail,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &models.Requisition{
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
	}, nil
}

// ResolveBudgetForDept picks a seeded budget id matching dept name (fallback BDG-2026-UT).
func (p *Procurement) ResolveBudgetForDept(ctx context.Context, dept string) (string, error) {
	dept = strings.TrimSpace(dept)
	var id string
	err := p.pool.QueryRow(ctx, `
		SELECT id FROM budgets
		WHERE ($1 <> '' AND dept ILIKE $1)
		ORDER BY id
		LIMIT 1`,
		dept,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return "BDG-2026-UT", nil
}
