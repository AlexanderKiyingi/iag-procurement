package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/models"
)

// epsilon guards the float band comparisons against rounding noise (amounts are
// stored as NUMERIC but compared as float64 here).
const approvalEpsilon = 0.005

// ApprovalTier is one band of the editable requisition_approval_tiers matrix.
type ApprovalTier struct {
	Tier         int      `json:"tier"`
	Label        string   `json:"label"`
	MinAmount    float64  `json:"minAmount"`
	MaxAmount    *float64 `json:"maxAmount,omitempty"`
	RequiredPerm string   `json:"requiredPerm"`
}

// ApprovalProgress summarises where a requisition stands in the tiered flow.
type ApprovalProgress struct {
	RequiredTiers []int  `json:"requiredTiers"`
	ApprovedTiers []int  `json:"approvedTiers"`
	NextTier      *int   `json:"nextTier,omitempty"`
	NextPerm      string `json:"nextPerm,omitempty"`
	Complete      bool   `json:"complete"`
}

// ListApprovalTiers returns the configured approval matrix ordered by tier.
func (p *Procurement) ListApprovalTiers(ctx context.Context) ([]ApprovalTier, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT tier, label, min_amount, max_amount, required_perm
		FROM requisition_approval_tiers ORDER BY tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalTier
	for rows.Next() {
		var t ApprovalTier
		if err := rows.Scan(&t.Tier, &t.Label, &t.MinAmount, &t.MaxAmount, &t.RequiredPerm); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Procurement) listApprovalTiersTx(ctx context.Context, tx pgx.Tx) ([]ApprovalTier, error) {
	rows, err := tx.Query(ctx, `
		SELECT tier, label, min_amount, max_amount, required_perm
		FROM requisition_approval_tiers ORDER BY tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalTier
	for rows.Next() {
		var t ApprovalTier
		if err := rows.Scan(&t.Tier, &t.Label, &t.MinAmount, &t.MaxAmount, &t.RequiredPerm); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// requiredTiers is the subset of the matrix whose lower bound sits below the
// requisition total — i.e. every band the amount reaches into, which must all
// sign off. A zero/negative total reaches no band and needs no tier signature.
func requiredTiers(tiers []ApprovalTier, total float64) []ApprovalTier {
	var out []ApprovalTier
	for _, t := range tiers {
		if total-t.MinAmount > approvalEpsilon {
			out = append(out, t)
		}
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func allRequiredApproved(required []ApprovalTier, approved []int) bool {
	for _, t := range required {
		if !containsInt(approved, t.Tier) {
			return false
		}
	}
	return true
}

func buildProgress(required []ApprovalTier, approved []int, complete bool) *ApprovalProgress {
	prog := &ApprovalProgress{ApprovedTiers: approved, Complete: complete}
	for _, t := range required {
		prog.RequiredTiers = append(prog.RequiredTiers, t.Tier)
		if prog.NextTier == nil && !containsInt(approved, t.Tier) {
			tier := t.Tier
			prog.NextTier = &tier
			prog.NextPerm = t.RequiredPerm
		}
	}
	if complete {
		prog.NextTier = nil
		prog.NextPerm = ""
	}
	return prog
}

// lockRequisitionForApproval selects-for-update the requisition plus the budget
// bookkeeping flags the outcome helpers need, mirroring UpdateRequisition's read.
func (p *Procurement) lockRequisitionForApproval(ctx context.Context, tx pgx.Tx, id string) (out models.Requisition, budgetCommitted, preReleased bool, pmReqID, pmOwner string, err error) {
	var (
		createdAt time.Time
		needed    *time.Time
		pmReq     *string
		pmOwn     *string
	)
	err = tx.QueryRow(ctx, `
		SELECT id, title, dept, requester, priority, status, created_at, needed_by, total, currency, budget_id,
		       pm_requisition_id, pm_workspace_owner, budget_committed, pre_released
		FROM requisitions WHERE id = $1 FOR UPDATE`, id,
	).Scan(
		&out.ID, &out.Title, &out.Dept, &out.Requester, &out.Priority, &out.Status,
		&createdAt, &needed, &out.Total, &out.Currency, &out.BudgetID,
		&pmReq, &pmOwn, &budgetCommitted, &preReleased,
	)
	if err == pgx.ErrNoRows {
		err = ErrNotFound
		return
	}
	if err != nil {
		return
	}
	out.CreatedAt = createdAt.UTC().Format("2006-01-02")
	if needed != nil {
		out.NeededBy = needed.UTC().Format("2006-01-02")
	}
	pmReqID = deref(pmReq)
	pmOwner = deref(pmOwn)
	return
}

func (p *Procurement) approvedTiers(ctx context.Context, tx pgx.Tx, reqID string) ([]int, error) {
	rows, err := tx.Query(ctx, `
		SELECT tier FROM requisition_approvals
		WHERE requisition_id = $1 AND decision = 'approved' ORDER BY tier`, reqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var t int
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Procurement) actorAlreadyApproved(ctx context.Context, tx pgx.Tx, reqID, actor string) (bool, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return false, nil // anonymous actors can't be deduped
	}
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM requisition_approvals
			WHERE requisition_id = $1 AND decision = 'approved' AND lower(actor) = lower($2)
		)`, reqID, actor,
	).Scan(&exists)
	return exists, err
}

func (p *Procurement) insertApprovalRow(ctx context.Context, tx pgx.Tx, reqID string, tier int, actor, decision, note string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO requisition_approvals (id, requisition_id, tier, actor, decision, note)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		newProcurementID("APPR"), reqID, tier, actor, decision, note,
	)
	return err
}

func (p *Procurement) auditTx(ctx context.Context, tx pgx.Tx, actor, action, target, detail string) error {
	if strings.TrimSpace(actor) == "" {
		actor = "unknown"
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ($1,$2,$3,$4)`, actor, action, target, detail)
	return err
}

// ApproveRequisitionTier records the calling approver's signature for the lowest
// not-yet-cleared required tier, then finalises the requisition once every
// required tier has signed. hasPerm reports whether the actor holds a given
// permission code (so the repo enforces the tier's required_perm without
// importing the HTTP layer). Distinct approvers are enforced — one person may
// sign at most one tier — and the requester may never approve their own
// requisition. On finalisation it reuses the existing budget pre-encumbrance and
// the procurement.requisition.approved outbox event, so downstream PO/finance
// flow is unchanged.
func (p *Procurement) ApproveRequisitionTier(ctx context.Context, id, actor string, hasPerm func(string) bool, note string) (*models.Requisition, *ApprovalProgress, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if hasPerm == nil {
		hasPerm = func(string) bool { return false }
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	req, budgetCommitted, preReleased, pmReqID, pmOwner, err := p.lockRequisitionForApproval(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Status)) {
	case "approved", "rejected", "ordered", "closed":
		return nil, nil, fmt.Errorf("%w: requisition is %s and can no longer be approved", ErrInvalidArgument, req.Status)
	}
	// Segregation of duties: the requester may not approve their own requisition.
	if sameActor(actor, req.Requester) {
		return nil, nil, fmt.Errorf("%w: requester cannot approve their own requisition", ErrForbidden)
	}

	tiers, err := p.listApprovalTiersTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	required := requiredTiers(tiers, req.Total)
	approved, err := p.approvedTiers(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}

	// Distinct approvers: an actor may sign at most one tier per requisition.
	if dup, err := p.actorAlreadyApproved(ctx, tx, id, actor); err != nil {
		return nil, nil, err
	} else if dup {
		return nil, nil, fmt.Errorf("%w: %s has already approved a tier on this requisition; a different approver is required", ErrForbidden, actor)
	}

	// Lowest required tier not yet approved.
	var next *ApprovalTier
	for i := range required {
		if !containsInt(approved, required[i].Tier) {
			next = &required[i]
			break
		}
	}

	finalize := false
	clearedTier := 0
	if next == nil {
		// No bands reached (e.g. zero-total requisition): a single sign-off from
		// anyone the route already authorised finalises it.
		finalize = true
	} else {
		if !hasPerm(next.RequiredPerm) {
			return nil, nil, fmt.Errorf("%w: approving tier %d (%s) requires permission %s", ErrForbidden, next.Tier, next.Label, next.RequiredPerm)
		}
		if err := p.insertApprovalRow(ctx, tx, id, next.Tier, actor, "approved", note); err != nil {
			return nil, nil, err
		}
		clearedTier = next.Tier
		approved = append(approved, next.Tier)
		finalize = allRequiredApproved(required, approved)
	}

	if finalize {
		if _, err := tx.Exec(ctx, `UPDATE requisitions SET status = 'approved' WHERE id = $1`, id); err != nil {
			return nil, nil, err
		}
		if err := p.applyBudgetCommitment(ctx, tx, id, "approved", budgetCommitted, preReleased, req.BudgetID, req.Total); err != nil {
			return nil, nil, err
		}
		if err := p.enqueueRequisitionOutcome(ctx, tx, "approved", id, pmReqID, pmOwner, actor, req.BudgetID); err != nil {
			return nil, nil, err
		}
		req.Status = "approved"
	} else if !strings.EqualFold(strings.TrimSpace(req.Status), "pending approval") {
		if _, err := tx.Exec(ctx, `UPDATE requisitions SET status = 'pending approval' WHERE id = $1`, id); err != nil {
			return nil, nil, err
		}
		req.Status = "pending approval"
	}

	detail := fmt.Sprintf("tier %d approved", clearedTier)
	if finalize {
		detail = fmt.Sprintf("tier %d approved (requisition fully approved)", clearedTier)
	}
	if err := p.auditTx(ctx, tx, actor, "approve", id, detail); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &req, buildProgress(required, approved, finalize), nil
}

// RejectRequisitionTiered rejects a requisition in the tiered flow. Any approver
// holding one of the required tier permissions can reject (a high-tier reviewer
// need not wait for lower tiers), and the requester may withdraw their own. The
// rejection is recorded in the ledger and releases any budget pre-encumbrance
// via the existing outcome helpers.
func (p *Procurement) RejectRequisitionTiered(ctx context.Context, id, actor string, hasPerm func(string) bool, note string) (*models.Requisition, *ApprovalProgress, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil, fmt.Errorf("%w: id is required", ErrInvalidArgument)
	}
	if hasPerm == nil {
		hasPerm = func(string) bool { return false }
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	req, budgetCommitted, preReleased, pmReqID, pmOwner, err := p.lockRequisitionForApproval(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Status)) {
	case "approved", "rejected", "ordered", "closed":
		return nil, nil, fmt.Errorf("%w: requisition is %s and can no longer be rejected", ErrInvalidArgument, req.Status)
	}

	tiers, err := p.listApprovalTiersTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	required := requiredTiers(tiers, req.Total)
	approved, err := p.approvedTiers(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}

	// Authorisation to reject: the requester (withdrawal), or any holder of a
	// required tier's permission. With no bands reached, the route's base
	// change_requisition grant already suffices.
	allowed := sameActor(actor, req.Requester) || len(required) == 0
	rejectTier := 0
	for _, t := range required {
		if hasPerm(t.RequiredPerm) {
			allowed = true
			if rejectTier == 0 {
				rejectTier = t.Tier
			}
		}
	}
	if !allowed {
		return nil, nil, fmt.Errorf("%w: rejecting this requisition requires a tier approval permission", ErrForbidden)
	}

	if err := p.insertApprovalRow(ctx, tx, id, rejectTier, actor, "rejected", note); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE requisitions SET status = 'rejected' WHERE id = $1`, id); err != nil {
		return nil, nil, err
	}
	if err := p.applyBudgetCommitment(ctx, tx, id, "rejected", budgetCommitted, preReleased, req.BudgetID, req.Total); err != nil {
		return nil, nil, err
	}
	if err := p.enqueueRequisitionOutcome(ctx, tx, "rejected", id, pmReqID, pmOwner, actor, req.BudgetID); err != nil {
		return nil, nil, err
	}
	if err := p.auditTx(ctx, tx, actor, "reject", id, "requisition rejected"); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	req.Status = "rejected"
	return &req, buildProgress(required, approved, false), nil
}
