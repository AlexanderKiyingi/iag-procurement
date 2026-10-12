package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

// vendorQualityCols are migration 033's columns, read wherever a vendor is.
const vendorQualityCols = `quality_status, quality_status_reason, qc_recommendation, qc_acceptance_rate, qc_reasons,
	COALESCE(to_char(qc_recommended_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')`

func vendorQualityDest(v *models.Vendor, reasons *[]byte) []any {
	return []any{&v.QualityStatus, &v.QualityStatusReason, &v.QCRecommendation, &v.QCAcceptanceRate, reasons, &v.QCRecommendedAt}
}

func finishVendorQuality(v *models.Vendor, reasons []byte) {
	v.QCReasons = []string{}
	_ = json.Unmarshal(reasons, &v.QCReasons)
}

var vendorQualityStatuses = map[string]bool{"": true, "approved": true, "conditional": true, "suspended": true}

/*
SetVendorQualityStatus records procurement's decision on a supplier's quality
standing. It is a decision someone answers for, so it needs a reason, and it
goes in the audit log with the advice quality had given at the time.
*/
func (p *Procurement) SetVendorQualityStatus(ctx context.Context, id, status, reason, auditUser string) error {
	id, status, reason = strings.TrimSpace(id), strings.ToLower(strings.TrimSpace(status)), strings.TrimSpace(reason)
	if !vendorQualityStatuses[status] {
		return fmt.Errorf("%w: quality status is approved, conditional, suspended or empty", ErrInvalidArgument)
	}
	if reason == "" {
		return fmt.Errorf("%w: a quality status decision needs a reason", ErrInvalidArgument)
	}
	if auditUser == "" {
		auditUser = "unknown"
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var advice string
	err = tx.QueryRow(ctx, `
		UPDATE vendors SET quality_status = $2, quality_status_reason = $3, quality_status_by = $4, quality_status_at = NOW()
		WHERE id::text = $1
		RETURNING qc_recommendation`, id, status, reason, auditUser).Scan(&advice)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return ErrNotFound
		}
		return err
	}
	if advice == "" {
		advice = "none"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail) VALUES ($1, $2, $3, $4)`,
		auditUser, "quality_status", id,
		fmt.Sprintf("quality status set to %q (quality advised %s): %s", status, advice, reason)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// QCRecommendation is quality's advice on one supplier (qc.supplier.scorecard).
type QCRecommendation struct {
	SupplierRef       string
	RecommendedStatus string
	AcceptanceRate    *float64
	Reasons           []string
	At                time.Time
}

/*
ApplyQCRecommendation stores quality's latest advice beside procurement's own
decision; it never changes the decision. The supplier is matched by vendor id
or by its SCM party business id. An older event than the one stored is
ignored, so a redelivered or reordered message cannot roll the advice back.
Reports whether a vendor matched.
*/
func (p *Procurement) ApplyQCRecommendation(ctx context.Context, rec QCRecommendation) (bool, error) {
	reasons, _ := json.Marshal(rec.Reasons)
	if rec.Reasons == nil {
		reasons = []byte("[]")
	}
	tag, err := p.pool.Exec(ctx, `
		UPDATE vendors SET qc_recommendation = $2, qc_acceptance_rate = $3, qc_reasons = $4::jsonb, qc_recommended_at = $5
		WHERE (id::text = $1 OR scm_business_id = $1)
		  AND (qc_recommended_at IS NULL OR qc_recommended_at <= $5)`,
		strings.TrimSpace(rec.SupplierRef), rec.RecommendedStatus, rec.AcceptanceRate, reasons, rec.At)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ErrSupplierSuspended is a purchase order to a supplier procurement suspended.
var ErrSupplierSuspended = fmt.Errorf("%w: this supplier is suspended for quality — lift the suspension before ordering", ErrConflict)

// supplierSuspended reports procurement's own suspension, not quality's advice.
func supplierSuspended(ctx context.Context, tx pgx.Tx, vendorID string) (bool, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT quality_status FROM vendors WHERE id::text = $1`, strings.TrimSpace(vendorID)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "suspended", nil
}
