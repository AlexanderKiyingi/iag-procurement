package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iag-procurement/backend/internal/models"
)

// NOTE: vendors + requisitions update/delete live in this file.

// UpdateVendor applies a partial update. Nil pointers mean "no change".
func (p *Procurement) UpdateVendor(
	ctx context.Context,
	id string,
	name, logo, category, contact, email, phone, country, terms *string,
	rating *float64,
	status *string,
	auditUser string,
) (*models.Vendor, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if name != nil && strings.TrimSpace(*name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE vendors SET
			name = COALESCE($2, name),
			logo = COALESCE($3, logo),
			category = COALESCE($4, category),
			contact = COALESCE($5, contact),
			email = COALESCE($6, email),
			phone = COALESCE($7, phone),
			country = COALESCE($8, country),
			terms = COALESCE($9, terms),
			rating = COALESCE($10, rating),
			status = COALESCE($11, status)
		WHERE id = $1`,
		id, name, logo, category, contact, email, phone, country, terms, rating, status,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "vendor updated",
	); err != nil {
		return nil, err
	}

	var out models.Vendor
	if err := tx.QueryRow(ctx, `
		SELECT id, name, logo, category, contact, email, phone, country, terms, rating, status, total_spend, open_pos
		FROM vendors WHERE id = $1`, id,
	).Scan(
		&out.ID, &out.Name, &out.Logo, &out.Category, &out.Contact, &out.Email, &out.Phone, &out.Country, &out.Terms,
		&out.Rating, &out.Status, &out.TotalSpend, &out.OpenPOs,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteVendor(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM vendors WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "vendor deleted",
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UpdateRequisition applies a partial update. Nil pointers mean "no change".
// If neededBy is present and points to nil, the date is cleared.
func (p *Procurement) UpdateRequisition(
	ctx context.Context,
	id string,
	title, dept, priority, status, currency, budgetID *string,
	neededBy **time.Time,
	total *float64,
	auditUser string,
) (*models.Requisition, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if title != nil && strings.TrimSpace(*title) == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidArgument)
	}
	if budgetID != nil && strings.TrimSpace(*budgetID) == "" {
		return nil, fmt.Errorf("%w: budgetId cannot be blank", ErrInvalidArgument)
	}

	var neededByArg interface{}
	if neededBy == nil {
		neededByArg = nil // no change
	} else if *neededBy == nil {
		neededByArg = (*time.Time)(nil) // clear
	} else {
		neededByArg = **neededBy
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE requisitions SET
			title = COALESCE($2, title),
			dept = COALESCE($3, dept),
			priority = COALESCE($4, priority),
			status = COALESCE($5, status),
			needed_by = CASE WHEN $6::date IS NULL THEN needed_by ELSE $6::date END,
			total = COALESCE($7, total),
			currency = COALESCE($8, currency),
			budget_id = COALESCE($9, budget_id)
		WHERE id = $1`,
		id, title, dept, priority, status, neededByArg, total, currency, budgetID,
	)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "update", id, "requisition updated",
	); err != nil {
		return nil, err
	}

	var (
		createdAt time.Time
		needed    *time.Time
	)
	out := models.Requisition{}
	if err := tx.QueryRow(ctx, `
		SELECT id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id
		FROM requisitions WHERE id = $1`, id,
	).Scan(
		&out.ID, &out.Title, &out.Dept, &out.Requester, &out.Priority, &out.Status,
		&createdAt, &needed, &out.Total, &out.Currency, &out.BudgetID,
	); err != nil {
		return nil, err
	}
	out.CreatedAt = createdAt.UTC().Format("2006-01-02")
	if needed != nil {
		out.NeededBy = needed.UTC().Format("2006-01-02")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Procurement) DeleteRequisition(ctx context.Context, id string, auditUser string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM requisitions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	if auditUser == "" {
		auditUser = "unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`,
		auditUser, "delete", id, "requisition deleted",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

