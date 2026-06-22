package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"iag-procurement/backend/internal/models"
)

// ErrProcurementRequestExists is returned when a (origin_system, origin_ref)
// request has already been imported.
var ErrProcurementRequestExists = errors.New("procurement request already imported")

// ImportProcurementRequest converts a generic inbound `procurement.requested`
// event (from stores/warehouse, fleet, or any other service) into a
// "Pending Approval" requisition, idempotent on (originSystem, originRef). It
// reuses the normal requisition lifecycle, so the imported request flows through
// approval → PO → receipt like any other. budgetID should be a resolved budget
// (e.g. via ResolveBudgetForDept); empty is stored as NULL and assigned later.
func (p *Procurement) ImportProcurementRequest(
	ctx context.Context,
	originSystem, originRef, title, dept, requester, priority string,
	total float64,
	currency, budgetID, justification, sourceEventID string,
) (*models.Requisition, error) {
	originSystem = strings.TrimSpace(originSystem)
	originRef = strings.TrimSpace(originRef)
	title = strings.TrimSpace(title)
	if originSystem == "" || originRef == "" {
		return nil, fmt.Errorf("%w: originSystem and originRef are required", ErrInvalidArgument)
	}
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}

	var exists bool
	if err := p.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM requisitions WHERE origin_system = $1 AND origin_ref = $2)`,
		originSystem, originRef,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrProcurementRequestExists
	}

	if currency == "" {
		currency = "USD"
	}
	if priority == "" {
		priority = "Medium"
	}
	status := "Pending Approval"

	id := newProcurementID("PR-2026")
	now := time.Now().UTC()
	createdDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO requisitions (id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id, justification, origin_system, origin_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,NULLIF($10,''),$11,$12,$13)`,
		id, title, strings.TrimSpace(dept), strings.TrimSpace(requester), priority, status, createdDay,
		total, currency, strings.TrimSpace(budgetID), strings.TrimSpace(justification), originSystem, originRef,
	); err != nil {
		// Concurrent duplicate: the unique (origin_system, origin_ref) index fired.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrProcurementRequestExists
		}
		return nil, err
	}

	detail := fmt.Sprintf("origin=%s ref=%s event=%s total=%.2f %s", originSystem, originRef, sourceEventID, total, currency)
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		originSystem, "procurement.request.import", id, detail,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &models.Requisition{
		ID:        id,
		Title:     title,
		Dept:      strings.TrimSpace(dept),
		Requester: strings.TrimSpace(requester),
		Priority:  priority,
		Status:    status,
		CreatedAt: createdDay.Format("2006-01-02"),
		Total:     total,
		Currency:  currency,
		BudgetID:  strings.TrimSpace(budgetID),
	}, nil
}

// GetRequisitionByOrigin looks up the requisition imported for a given source
// system + reference (e.g. originSystem="fleet", originRef="FREQ-…"). It lets
// the originating service reconcile its own record against procurement's
// approval state without holding the procurement requisition id. Returns
// ErrNotFound when no import exists yet.
func (p *Procurement) GetRequisitionByOrigin(ctx context.Context, originSystem, originRef string) (*models.Requisition, error) {
	originSystem = strings.TrimSpace(originSystem)
	originRef = strings.TrimSpace(originRef)
	if originSystem == "" || originRef == "" {
		return nil, fmt.Errorf("%w: originSystem and originRef are required", ErrInvalidArgument)
	}
	var (
		r               models.Requisition
		created, needed *time.Time
	)
	err := p.pool.QueryRow(ctx, `
		SELECT id, title, dept, requester, priority, status, created_at, needed_by, total, currency, COALESCE(budget_id, '')
		FROM requisitions WHERE origin_system = $1 AND origin_ref = $2`,
		originSystem, originRef,
	).Scan(&r.ID, &r.Title, &r.Dept, &r.Requester, &r.Priority, &r.Status,
		&created, &needed, &r.Total, &r.Currency, &r.BudgetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if created != nil {
		r.CreatedAt = created.UTC().Format("2006-01-02")
	}
	if needed != nil {
		r.NeededBy = needed.UTC().Format("2006-01-02")
	}
	return &r, nil
}
