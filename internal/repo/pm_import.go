package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

var ErrPMRequisitionExists = errors.New("pm requisition already imported")

// ImportPMRequisition creates a procurement requisition from a PM workspace event (idempotent on pmID).
// pmOwnerUserID is stored so approval/rejection events can route back to the originating PM workspace.
func (p *Procurement) ImportPMRequisition(
	ctx context.Context,
	pmID, pmOwnerUserID, title, dept, requester, priority, status string,
	total float64,
	currency, budgetID, payee, justification, sourceEventID string,
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
	if strings.TrimSpace(budgetID) == "" {
		return nil, ErrNoBudget
	}

	docNo := newDocNo("PR-2026")
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	pmOwner := strings.TrimSpace(pmOwnerUserID)
	var pmOwnerArg any
	if pmOwner == "" {
		pmOwnerArg = nil
	} else {
		pmOwnerArg = pmOwner
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO requisitions (doc_no, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id, pm_requisition_id, pm_workspace_owner, payee, justification)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		docNo, title, dept, requester, priority, status, createdDay, total, currency, budgetID, pmID, pmOwnerArg,
		strings.TrimSpace(payee), strings.TrimSpace(justification),
	).Scan(&id); err != nil {
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

// PMLink represents the PM workspace fields stored on a procurement requisition.
// Both fields may be empty if the requisition was not imported from PM.
type PMLink struct {
	PMRequisitionID  string
	PMWorkspaceOwner string
}

// GetPMLink returns the PM workspace identifiers for a procurement requisition.
// Returns empty fields (and nil error) if the requisition is not PM-sourced.
// Returns ErrNotFound if the requisition does not exist.
func (p *Procurement) GetPMLink(ctx context.Context, requisitionID string) (PMLink, error) {
	requisitionID = strings.TrimSpace(requisitionID)
	if requisitionID == "" {
		return PMLink{}, fmt.Errorf("requisition id required")
	}
	var pmID, pmOwner *string
	err := p.pool.QueryRow(ctx, `
		SELECT pm_requisition_id, pm_workspace_owner
		FROM requisitions
		WHERE id = $1`,
		requisitionID,
	).Scan(&pmID, &pmOwner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PMLink{}, ErrNotFound
		}
		return PMLink{}, err
	}
	link := PMLink{}
	if pmID != nil {
		link.PMRequisitionID = *pmID
	}
	if pmOwner != nil {
		link.PMWorkspaceOwner = *pmOwner
	}
	return link, nil
}

// ErrNoBudget means no budget could be found for an imported request's
// department and no default is configured. The request cannot be stored —
// requisitions.budget_id references budgets(id), and approval reads it — so
// the consumer drops the event with this reason instead of retrying it.
var ErrNoBudget = errors.New("no budget matches the department and PROCUREMENT_DEFAULT_BUDGET_ID is not set")

// ResolveBudgetForDept picks the budget an imported request is charged to:
// the first whose dept matches, else PROCUREMENT_DEFAULT_BUDGET_ID when it
// names a real budget, else ErrNoBudget.
//
// It used to fall back to the literal "BDG-2026-UT". Since 027 budget ids
// are UUIDs, so that value failed the insert's cast and every unmatched
// import errored and was retried on each restart, for ever.
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
	if def := strings.TrimSpace(os.Getenv("PROCUREMENT_DEFAULT_BUDGET_ID")); def != "" {
		var found string
		err := p.pool.QueryRow(ctx, `SELECT id::text FROM budgets WHERE id::text = $1`, def).Scan(&found)
		if err == nil {
			return found, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	return "", ErrNoBudget
}
