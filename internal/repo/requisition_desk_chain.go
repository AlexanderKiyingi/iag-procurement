package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alvor-technologies/iag-platform-go/approvalchain"
	"github.com/jackc/pgx/v5"
)

// Desk chains sit beside the amount-band tiers of migration 018 rather than
// replacing them. A requisition uses one mechanism or the other: it has a row in
// requisition_approval_state, or it walks the tier matrix. Mixing the two on one
// requisition would let a signature count twice, so LoadDeskState is the guard
// every desk endpoint checks first.

// DeskChainKey names the chains this service routes.
const (
	ChainRequisition         = "requisition"
	ChainMaterialStores      = "material.stores"
	ChainMaterialProcurement = "material.procurement"
)

// DeskEngine builds an approvalchain engine from the editable desk matrix.
//
// Chains are database rows, not constants, because role names and escalation
// bands are deployment data — the same reason requisition_approval_tiers is a
// table. Definitions are read once and cached; call ReloadDeskChains after an
// edit.
func (p *Procurement) DeskEngine(ctx context.Context) (*approvalchain.Engine, error) {
	p.deskMu.RLock()
	eng := p.deskEngine
	p.deskMu.RUnlock()
	if eng != nil {
		return eng, nil
	}
	return p.ReloadDeskChains(ctx)
}

// ReloadDeskChains re-reads the desk matrix and rebuilds the engine.
func (p *Procurement) ReloadDeskChains(ctx context.Context) (*approvalchain.Engine, error) {
	chains, err := p.loadDeskChains(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := approvalchain.NewRegistry(chains...)
	if err != nil {
		// A matrix that cannot route is a configuration error, not a request
		// error — surface it as such rather than 500ing on every approval.
		return nil, fmt.Errorf("%w: approval desk matrix: %s", ErrInvalidArgument, err.Error())
	}
	eng := approvalchain.NewEngine(reg)
	p.deskMu.Lock()
	p.deskEngine = eng
	p.deskMu.Unlock()
	return eng, nil
}

func (p *Procurement) loadDeskChains(ctx context.Context) ([]approvalchain.Chain, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT chain_key, desk, label, role_patterns, min_amount,
		       required_perm, action_label, status_label, scope_by, no_repeat_approver
		FROM requisition_approval_desks
		ORDER BY chain_key, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byChain := make(map[string][]approvalchain.Desk)
	noRepeatByChain := make(map[string]bool)
	var order []string
	for rows.Next() {
		var (
			chainKey, desk, label      string
			patterns                   []string
			minAmount                  float64
			perm, actionLbl, statusLbl string
			scopeBy                    string
			noRepeat                   bool
		)
		if err := rows.Scan(&chainKey, &desk, &label, &patterns, &minAmount,
			&perm, &actionLbl, &statusLbl, &scopeBy, &noRepeat); err != nil {
			return nil, err
		}
		if _, seen := byChain[chainKey]; !seen {
			order = append(order, chainKey)
		}
		// Chain-level flag carried on desk rows: any row setting it turns it on.
		if noRepeat {
			noRepeatByChain[chainKey] = true
		}
		byChain[chainKey] = append(byChain[chainKey], approvalchain.Desk{
			Key:          approvalchain.DeskKey(desk),
			Label:        label,
			RolePatterns: patterns,
			MinAmount:    minAmount,
			RequiredPerm: perm,
			ActionLabel:  actionLbl,
			StatusLabel:  statusLbl,
			ScopeBy:      scopeBy,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]approvalchain.Chain, 0, len(order))
	for _, key := range order {
		out = append(out, approvalchain.Chain{
			Key:              key,
			Label:            deskChainLabel(byChain[key]),
			TerminalLabel:    terminalLabel(byChain[key]),
			Desks:            byChain[key],
			NoRepeatApprover: noRepeatByChain[key],
		})
	}
	return out, nil
}

// deskChainLabel renders the "Requestor → PM → … → Paid" summary from the desks
// themselves, so the label can never drift from the matrix it describes.
func deskChainLabel(desks []approvalchain.Desk) string {
	parts := make([]string, 0, len(desks)+2)
	parts = append(parts, "Requestor")
	for _, d := range desks {
		parts = append(parts, d.Label)
	}
	parts = append(parts, terminalLabel(desks))
	return strings.Join(parts, " → ")
}

func terminalLabel(desks []approvalchain.Desk) string {
	if len(desks) == 0 {
		return "Approved"
	}
	return desks[len(desks)-1].PassedStatus()
}

// DeskRow is one editable row of the desk matrix.
type DeskRow struct {
	ChainKey     string   `json:"chainKey"`
	Position     int      `json:"position"`
	Desk         string   `json:"desk"`
	Label        string   `json:"label"`
	RolePatterns []string `json:"rolePatterns"`
	MinAmount    float64  `json:"minAmount"`
	RequiredPerm string   `json:"requiredPerm"`
	ActionLabel  string   `json:"actionLabel"`
	StatusLabel  string   `json:"statusLabel"`
	ScopeBy      string   `json:"scopeBy,omitempty"`
	// NoRepeatApprover is a chain-level flag stored on every row of the chain.
	NoRepeatApprover bool `json:"noRepeatApprover,omitempty"`
}

// ListDeskMatrix returns the desk matrix as editable rows.
func (p *Procurement) ListDeskMatrix(ctx context.Context) ([]DeskRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT chain_key, position, desk, label, role_patterns, min_amount,
		       required_perm, action_label, status_label, scope_by, no_repeat_approver
		FROM requisition_approval_desks ORDER BY chain_key, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DeskRow, 0)
	for rows.Next() {
		var r DeskRow
		if err := rows.Scan(&r.ChainKey, &r.Position, &r.Desk, &r.Label, &r.RolePatterns,
			&r.MinAmount, &r.RequiredPerm, &r.ActionLabel, &r.StatusLabel,
			&r.ScopeBy, &r.NoRepeatApprover); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceDeskChain rewrites one chain's desks and reloads the engine.
//
// The whole chain is replaced rather than patched desk by desk, because a chain
// is only meaningful as an ordered whole — a half-applied edit could leave a
// request sitting on a desk that no longer exists.
//
// The new definition is validated by building a registry from it before
// anything is written, so a matrix that cannot route is rejected at the API
// rather than discovered by the next approver.
func (p *Procurement) ReplaceDeskChain(ctx context.Context, chainKey string, desks []DeskRow) ([]DeskRow, error) {
	chainKey = strings.TrimSpace(chainKey)
	if chainKey == "" {
		return nil, fmt.Errorf("%w: chain key is required", ErrInvalidArgument)
	}
	if len(desks) == 0 {
		return nil, fmt.Errorf("%w: a chain needs at least one desk", ErrInvalidArgument)
	}

	candidate := make([]approvalchain.Desk, 0, len(desks))
	for _, d := range desks {
		candidate = append(candidate, approvalchain.Desk{
			Key:          approvalchain.DeskKey(strings.TrimSpace(d.Desk)),
			Label:        d.Label,
			RolePatterns: d.RolePatterns,
			MinAmount:    d.MinAmount,
			RequiredPerm: d.RequiredPerm,
			ActionLabel:  d.ActionLabel,
			StatusLabel:  d.StatusLabel,
			ScopeBy:      d.ScopeBy,
		})
	}
	if _, err := approvalchain.NewRegistry(approvalchain.Chain{
		Key:              chainKey,
		Label:            deskChainLabel(candidate),
		TerminalLabel:    terminalLabel(candidate),
		Desks:            candidate,
		NoRepeatApprover: desks[0].NoRepeatApprover,
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err.Error())
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// A chain cannot be re-cut while requests are walking it: the desks they
	// are sitting on may not survive the edit.
	var inFlight int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM requisition_approval_state
		WHERE chain_key = $1 AND status IN ('in_flight', 'returned_for_amendment')`,
		chainKey).Scan(&inFlight); err != nil {
		return nil, err
	}
	if inFlight > 0 {
		return nil, fmt.Errorf(
			"%w: %d request(s) are still walking %q; finish or cancel them before re-cutting the chain",
			ErrConflict, inFlight, chainKey)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM requisition_approval_desks WHERE chain_key = $1`, chainKey); err != nil {
		return nil, err
	}
	for i, d := range desks {
		if _, err := tx.Exec(ctx, `
			INSERT INTO requisition_approval_desks
				(chain_key, position, desk, label, role_patterns, min_amount,
				 required_perm, action_label, status_label, scope_by, no_repeat_approver)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			chainKey, i+1, strings.TrimSpace(d.Desk), d.Label, d.RolePatterns,
			d.MinAmount, d.RequiredPerm, d.ActionLabel, d.StatusLabel,
			d.ScopeBy, d.NoRepeatApprover); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Rebuild immediately so the edit takes effect without a restart.
	if _, err := p.ReloadDeskChains(ctx); err != nil {
		return nil, err
	}
	return p.ListDeskMatrix(ctx)
}

// DeleteDeskChain removes a chain, refusing while requests still walk it.
func (p *Procurement) DeleteDeskChain(ctx context.Context, chainKey string) error {
	chainKey = strings.TrimSpace(chainKey)
	if chainKey == "" {
		return fmt.Errorf("%w: chain key is required", ErrInvalidArgument)
	}
	var open int
	if err := p.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM requisition_approval_state
		WHERE chain_key = $1 AND status IN ('draft', 'in_flight', 'returned_for_amendment')`,
		chainKey).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return fmt.Errorf("%w: %d open request(s) still use %q", ErrConflict, open, chainKey)
	}
	ct, err := p.pool.Exec(ctx, `DELETE FROM requisition_approval_desks WHERE chain_key = $1`, chainKey)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = p.ReloadDeskChains(ctx)
	return err
}

// DeskState is a requisition's chain state plus the rendered progress.
type DeskState struct {
	RequisitionID string                 `json:"requisitionId"`
	State         approvalchain.State    `json:"state"`
	Progress      approvalchain.Progress `json:"progress"`
}

// LoadDeskState reads a requisition's chain state and history.
func (p *Procurement) LoadDeskState(ctx context.Context, requisitionID string) (approvalchain.State, error) {
	return p.loadDeskStateTx(ctx, p.pool, requisitionID)
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (p *Procurement) loadDeskStateTx(ctx context.Context, q querier, requisitionID string) (approvalchain.State, error) {
	var (
		s    approvalchain.State
		desk string
		skip []string
	)
	err := q.QueryRow(ctx, `
		SELECT chain_key, status, desk, stage_index, amount, skip, requester, scope
		FROM requisition_approval_state WHERE requisition_id = $1`, requisitionID).
		Scan(&s.ChainKey, &s.Status, &desk, &s.StageIndex, &s.Amount, &skip, &s.Requester, &s.Scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvalchain.State{}, ErrNotFound
	}
	if err != nil {
		return approvalchain.State{}, err
	}
	s.Desk = approvalchain.DeskKey(desk)
	for _, k := range skip {
		s.Skip = append(s.Skip, approvalchain.DeskKey(k))
	}

	rows, err := q.Query(ctx, `
		SELECT desk, desk_label, action, actor, actor_role, reason, override, acted_at
		FROM requisition_approval_steps WHERE requisition_id = $1 ORDER BY seq`, requisitionID)
	if err != nil {
		return approvalchain.State{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			st      approvalchain.Step
			dk, act string
			actedAt time.Time
		)
		if err := rows.Scan(&dk, &st.DeskLabel, &act, &st.Actor, &st.ActorRole, &st.Reason, &st.Override, &actedAt); err != nil {
			return approvalchain.State{}, err
		}
		st.Desk = approvalchain.DeskKey(dk)
		st.Action = approvalchain.Action(act)
		st.At = actedAt.UTC()
		s.History = append(s.History, st)
	}
	return s, rows.Err()
}

// OpenDeskChain starts a requisition on a desk chain in draft.
//
// It refuses if the requisition already has tier signatures: a requisition that
// has begun one approval mechanism must finish on it, or the two ledgers would
// each hold half the story.
func (p *Procurement) OpenDeskChain(
	ctx context.Context, requisitionID, chainKey, requester string, skip []approvalchain.DeskKey,
) (approvalchain.State, error) {
	requisitionID = strings.TrimSpace(requisitionID)
	if requisitionID == "" {
		return approvalchain.State{}, fmt.Errorf("%w: requisition id is required", ErrInvalidArgument)
	}
	// An anonymous requester would open a chain nobody could submit: the
	// four-eyes comparison treats a blank identity as matching nobody, so the
	// request would sit in draft forever. Fail here, where the cause is obvious.
	if strings.TrimSpace(requester) == "" {
		return approvalchain.State{}, fmt.Errorf(
			"%w: the caller has no identity, so no one could submit this chain", ErrInvalidArgument)
	}
	eng, err := p.DeskEngine(ctx)
	if err != nil {
		return approvalchain.State{}, err
	}
	if _, ok := eng.Registry().Get(chainKey); !ok {
		return approvalchain.State{}, fmt.Errorf("%w: unknown approval chain %q", ErrInvalidArgument, chainKey)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return approvalchain.State{}, err
	}
	defer tx.Rollback(ctx)

	var total float64
	if err := tx.QueryRow(ctx,
		`SELECT total FROM requisitions WHERE id = $1`, requisitionID).Scan(&total); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return approvalchain.State{}, ErrNotFound
		}
		return approvalchain.State{}, err
	}

	var tierSignatures int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM requisition_approvals WHERE requisition_id = $1 AND decision = 'approved'`,
		requisitionID).Scan(&tierSignatures); err != nil {
		return approvalchain.State{}, err
	}
	if tierSignatures > 0 {
		return approvalchain.State{}, fmt.Errorf(
			"%w: this requisition already has tiered approvals; it cannot switch to a desk chain", ErrConflict)
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM requisition_approval_state WHERE requisition_id = $1)`,
		requisitionID).Scan(&exists); err != nil {
		return approvalchain.State{}, err
	}
	if exists {
		return approvalchain.State{}, fmt.Errorf("%w: requisition is already on a desk chain", ErrConflict)
	}

	scope, err := p.requisitionScope(ctx, tx, requisitionID)
	if err != nil {
		return approvalchain.State{}, err
	}

	state := approvalchain.New(chainKey, requester, approvalchain.Options{
		Amount: total, Skip: skip, Scope: scope,
	})
	if err := insertDeskState(ctx, tx, requisitionID, state); err != nil {
		return approvalchain.State{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return approvalchain.State{}, err
	}
	return state, nil
}

// DeskTransition is one of the engine's transitions.
type DeskTransition func(*approvalchain.Engine, approvalchain.State) (approvalchain.State, error)

// requisitionStatusFor maps a chain position onto the status vocabulary the rest
// of the service already uses. The tiered path writes 'pending approval' and
// 'approved'/'rejected'; the desk path must write the same strings or every
// consumer filtering on them would silently miss desk-chain requisitions.
func requisitionStatusFor(s approvalchain.Status) string {
	switch s {
	case approvalchain.StatusApproved:
		return "approved"
	case approvalchain.StatusRejected:
		return "rejected"
	case approvalchain.StatusCancelled:
		return "cancelled"
	case approvalchain.StatusReturned:
		return "returned for amendment"
	case approvalchain.StatusDraft:
		return "draft"
	default:
		return "pending approval"
	}
}

// ApplyDeskTransition applies fn to a requisition's chain state and commits the
// result together with everything an approval outcome implies.
//
// It reuses lockRequisitionForApproval so the desk path takes the same row lock
// the tiered path does. That serialises concurrent approvers — two desks
// clicking together cannot both read the same stage_index and each advance it — and
// it serialises the two approval mechanisms against each other.
//
// On a terminal outcome it performs the same three side effects the tiered path
// performs inside the transaction: the budget commitment, the outbox event, and
// the audit row. Doing any of them after the commit would let an approval exist
// with its budget un-encumbered or its event lost.
func (p *Procurement) ApplyDeskTransition(
	ctx context.Context, requisitionID string, fn DeskTransition,
) (approvalchain.State, error) {
	eng, err := p.DeskEngine(ctx)
	if err != nil {
		return approvalchain.State{}, err
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return approvalchain.State{}, err
	}
	defer tx.Rollback(ctx)

	req, budgetCommitted, preReleased, pmReqID, pmOwner, err := p.lockRequisitionForApproval(ctx, tx, requisitionID)
	if err != nil {
		return approvalchain.State{}, err
	}

	before, err := p.loadDeskStateTx(ctx, tx, requisitionID)
	if err != nil {
		return approvalchain.State{}, err
	}

	// The requisition total can be edited while the chain is in flight, so the
	// chain is re-evaluated against the live figure before every transition.
	// Without this a requisition opened at 1M and raised to 50M would still walk
	// the small-request desks and never reach GM or CEO.
	rebased, err := eng.Rebase(before, req.Total)
	if err != nil {
		return approvalchain.State{}, mapChainErr(err)
	}

	after, err := fn(eng, rebased)
	if err != nil {
		return approvalchain.State{}, mapChainErr(err)
	}

	if err := updateDeskState(ctx, tx, requisitionID, after); err != nil {
		return approvalchain.State{}, err
	}
	// Only the steps this transition added are written; earlier history is
	// immutable and already on disk.
	for i := len(before.History); i < len(after.History); i++ {
		if err := insertDeskStep(ctx, tx, requisitionID, i, after.History[i]); err != nil {
			return approvalchain.State{}, err
		}
	}

	status := requisitionStatusFor(after.Status)
	if _, err := tx.Exec(ctx,
		`UPDATE requisitions SET status = $2 WHERE id = $1`, requisitionID, status); err != nil {
		return approvalchain.State{}, err
	}

	if outcome := terminalOutcome(after.Status); outcome != "" {
		if err := p.applyBudgetCommitment(
			ctx, tx, requisitionID, outcome, budgetCommitted, preReleased, req.BudgetID, req.Total); err != nil {
			return approvalchain.State{}, err
		}
		if err := p.enqueueRequisitionOutcome(
			ctx, tx, outcome, requisitionID, pmReqID, pmOwner, lastActor(after), req.BudgetID); err != nil {
			return approvalchain.State{}, err
		}
	}

	if err := p.auditTx(
		ctx, tx, lastActor(after), deskAuditAction(after), requisitionID, deskAuditDetail(after)); err != nil {
		return approvalchain.State{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return approvalchain.State{}, err
	}
	return after, nil
}

// terminalOutcome is the budget/event outcome word for a finished chain, or ""
// while it is still moving. Cancelled releases like a rejection: the money was
// pre-encumbered and is not going to be spent.
func terminalOutcome(s approvalchain.Status) string {
	switch s {
	case approvalchain.StatusApproved:
		return "approved"
	case approvalchain.StatusRejected, approvalchain.StatusCancelled:
		return "rejected"
	default:
		return ""
	}
}

func lastActor(s approvalchain.State) string {
	if len(s.History) == 0 {
		return ""
	}
	return s.History[len(s.History)-1].Actor
}

// deskAuditAction is the verb recorded in the service audit trail. It follows
// the transition rather than being fixed at "approve", so a rejection does not
// read as an approval in /audit.
func deskAuditAction(s approvalchain.State) string {
	if len(s.History) == 0 {
		return "approve"
	}
	switch s.History[len(s.History)-1].Action {
	case approvalchain.ActionReject:
		return "reject"
	case approvalchain.ActionAmend:
		return "amend"
	case approvalchain.ActionCancel:
		return "cancel"
	case approvalchain.ActionSubmit:
		return "submit"
	default:
		return "approve"
	}
}

// deskAuditDetail describes the transition for the service audit trail, so
// /audit reads the same for desk approvals as it does for tiered ones.
func deskAuditDetail(s approvalchain.State) string {
	if len(s.History) == 0 {
		return "desk chain updated"
	}
	last := s.History[len(s.History)-1]
	detail := fmt.Sprintf("%s at %s desk", last.Action, last.DeskLabel)
	if last.Reason != "" {
		detail += ": " + last.Reason
	}
	if last.Override {
		detail += " (admin override)"
	}
	if s.Status == approvalchain.StatusApproved {
		detail += " — requisition fully approved"
	}
	return detail
}

func insertDeskState(ctx context.Context, tx pgx.Tx, id string, s approvalchain.State) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO requisition_approval_state
			(requisition_id, chain_key, status, desk, stage_index, amount, skip, requester, scope)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, s.ChainKey, string(s.Status), string(s.Desk), s.StageIndex, s.Amount,
		deskKeyStrings(s.Skip), s.Requester, scopeJSON(s.Scope))
	return err
}

func updateDeskState(ctx context.Context, tx pgx.Tx, id string, s approvalchain.State) error {
	_, err := tx.Exec(ctx, `
		UPDATE requisition_approval_state
		SET status = $2, desk = $3, stage_index = $4, amount = $5, skip = $6, updated_at = NOW()
		WHERE requisition_id = $1`,
		id, string(s.Status), string(s.Desk), s.StageIndex, s.Amount, deskKeyStrings(s.Skip))
	return err
}

func insertDeskStep(ctx context.Context, tx pgx.Tx, id string, seq int, st approvalchain.Step) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO requisition_approval_steps
			(requisition_id, seq, desk, desk_label, action, actor, actor_role, reason, override, acted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, seq, string(st.Desk), st.DeskLabel, string(st.Action),
		st.Actor, st.ActorRole, st.Reason, st.Override, st.At)
	return err
}

// scopeJSON renders a scope map for the JSONB column, never nil so the NOT NULL
// default is never fought with.
func scopeJSON(scope map[string]string) map[string]string {
	if scope == nil {
		return map[string]string{}
	}
	return scope
}

// requisitionScope is the ownership a requisition carries, in the keys desks
// scope on. pm_workspace_owner is the assigned project manager where the
// requisition came from project management; dept is its department.
func (p *Procurement) requisitionScope(ctx context.Context, tx pgx.Tx, id string) (map[string]string, error) {
	var pmOwner, dept *string
	if err := tx.QueryRow(ctx, `
		SELECT pm_workspace_owner, dept FROM requisitions WHERE id = $1`, id).Scan(&pmOwner, &dept); err != nil {
		return nil, err
	}
	scope := map[string]string{}
	if pmOwner != nil && strings.TrimSpace(*pmOwner) != "" {
		scope["project_owner"] = strings.TrimSpace(*pmOwner)
	}
	if dept != nil && strings.TrimSpace(*dept) != "" {
		scope["department"] = strings.TrimSpace(*dept)
	}
	return scope, nil
}

func deskKeyStrings(keys []approvalchain.DeskKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, string(k))
	}
	return out
}

// DeskQueueItem is one row of a desk queue.
type DeskQueueItem struct {
	RequisitionID string                 `json:"requisitionId"`
	Title         string                 `json:"title"`
	Dept          string                 `json:"dept"`
	Requester     string                 `json:"requester"`
	Total         float64                `json:"total"`
	Currency      string                 `json:"currency"`
	NeededBy      *time.Time             `json:"neededBy,omitempty"`
	Progress      approvalchain.Progress `json:"progress"`
}

// DeskQueue lists the requisitions waiting on any desk this actor holds.
//
// The filter is computed from the chain matrix rather than from a role column on
// the requisition, so adding a role to a desk immediately changes what that role
// sees without touching a single request row.
func (p *Procurement) DeskQueue(ctx context.Context, actor approvalchain.Actor) ([]DeskQueueItem, error) {
	eng, err := p.DeskEngine(ctx)
	if err != nil {
		return nil, err
	}
	byChain := eng.Registry().DesksForActor(actor)
	if len(byChain) == 0 {
		return []DeskQueueItem{}, nil
	}

	chainKeys := make([]string, 0, len(byChain))
	deskKeys := make([]string, 0)
	for chain, desks := range byChain {
		chainKeys = append(chainKeys, chain)
		for _, d := range desks {
			deskKeys = append(deskKeys, string(d))
		}
	}

	rows, err := p.pool.Query(ctx, `
		SELECT s.requisition_id, r.title, r.dept, s.requester, r.total, r.currency, r.needed_by,
		       s.chain_key, s.status, s.desk, s.stage_index, s.amount, s.skip, s.scope
		FROM requisition_approval_state s
		JOIN requisitions r ON r.id = s.requisition_id
		WHERE s.status = 'in_flight'
		  AND s.chain_key = ANY($1)
		  AND s.desk = ANY($2)
		ORDER BY r.needed_by NULLS LAST, s.updated_at`, chainKeys, deskKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type queued struct {
		item  DeskQueueItem
		state approvalchain.State
	}
	queue := make([]queued, 0)
	ids := make([]string, 0)
	for rows.Next() {
		var (
			q    queued
			desk string
			skip []string
		)
		if err := rows.Scan(&q.item.RequisitionID, &q.item.Title, &q.item.Dept, &q.item.Requester,
			&q.item.Total, &q.item.Currency, &q.item.NeededBy,
			&q.state.ChainKey, &q.state.Status, &desk, &q.state.StageIndex, &q.state.Amount,
			&skip, &q.state.Scope); err != nil {
			return nil, err
		}
		q.state.Desk = approvalchain.DeskKey(desk)
		q.state.Requester = q.item.Requester
		for _, k := range skip {
			q.state.Skip = append(q.state.Skip, approvalchain.DeskKey(k))
		}
		queue = append(queue, q)
		ids = append(ids, q.item.RequisitionID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(queue) == 0 {
		return []DeskQueueItem{}, nil
	}

	// History for the whole page in one query rather than two per row. A desk
	// queue is read on every dashboard load, so the N+1 this replaces was the
	// hottest path in the feature.
	history, err := p.loadDeskHistories(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Actionable enforces four-eyes on top of the SQL filter: a requisition
	// sitting on a desk the actor holds is still not theirs to approve if they
	// raised it.
	out := make([]DeskQueueItem, 0, len(queue))
	for _, q := range queue {
		q.state.History = history[q.item.RequisitionID]
		if !eng.Actionable(q.state, actor) {
			continue
		}
		prog, err := eng.Progress(q.state)
		if err != nil {
			return nil, err
		}
		q.item.Progress = prog
		out = append(out, q.item)
	}
	return out, nil
}

// loadDeskHistories reads the step history for many requisitions at once.
func (p *Procurement) loadDeskHistories(ctx context.Context, ids []string) (map[string][]approvalchain.Step, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT requisition_id, desk, desk_label, action, actor, actor_role, reason, override, acted_at
		FROM requisition_approval_steps
		WHERE requisition_id = ANY($1)
		ORDER BY requisition_id, seq`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]approvalchain.Step, len(ids))
	for rows.Next() {
		var (
			reqID, dk, act string
			st             approvalchain.Step
			actedAt        time.Time
		)
		if err := rows.Scan(&reqID, &dk, &st.DeskLabel, &act, &st.Actor,
			&st.ActorRole, &st.Reason, &st.Override, &actedAt); err != nil {
			return nil, err
		}
		st.Desk = approvalchain.DeskKey(dk)
		st.Action = approvalchain.Action(act)
		st.At = actedAt.UTC()
		out[reqID] = append(out[reqID], st)
	}
	return out, rows.Err()
}

// DeskRecipients resolves the people who hold a desk, for notification.
//
// It is the same question Actionable answers per request, asked the other way
// round: rather than "can this actor act", it is "who could". Group names are
// this service's role names, so the desk's own role patterns decide — meaning a
// desk re-pointed at a different role immediately notifies different people,
// with no separate recipient list to keep in step.
//
// The requester is excluded: four-eyes means they could not action it anyway,
// and telling someone their own request awaits them is noise.
func (p *Procurement) DeskRecipients(
	ctx context.Context, chainKey string, desk approvalchain.DeskKey, excludeRequester string,
) ([]string, error) {
	eng, err := p.DeskEngine(ctx)
	if err != nil {
		return nil, err
	}
	chain, ok := eng.Registry().Get(chainKey)
	if !ok {
		return nil, ErrNotFound
	}
	d, ok := chain.Desk(desk)
	if !ok {
		return nil, ErrNotFound
	}

	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT u.email, g.name
		FROM auth_users u
		JOIN auth_user_groups ug ON ug.user_id = u.id
		JOIN auth_groups g ON g.id = ug.group_id
		WHERE u.is_active`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	out := make([]string, 0)
	for rows.Next() {
		var email, group string
		if err := rows.Scan(&email, &group); err != nil {
			return nil, err
		}
		if !d.Matches(group) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(excludeRequester)) {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	return out, rows.Err()
}

// DeskProgress renders one requisition's tracker.
func (p *Procurement) DeskProgress(ctx context.Context, requisitionID string) (*DeskState, error) {
	eng, err := p.DeskEngine(ctx)
	if err != nil {
		return nil, err
	}
	st, err := p.LoadDeskState(ctx, requisitionID)
	if err != nil {
		return nil, err
	}
	prog, err := eng.Progress(st)
	if err != nil {
		return nil, mapChainErr(err)
	}
	return &DeskState{RequisitionID: requisitionID, State: st, Progress: prog}, nil
}

// mapChainErr translates engine errors onto the repo error vocabulary the
// handlers already map to HTTP status codes.
func mapChainErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, approvalchain.ErrForbidden),
		errors.Is(err, approvalchain.ErrSelfApproval),
		errors.Is(err, approvalchain.ErrNotRequester):
		return fmt.Errorf("%w: %s", ErrForbidden, err.Error())
	case errors.Is(err, approvalchain.ErrReasonRequired):
		return fmt.Errorf("%w: a reason is required", ErrInvalidArgument)
	case errors.Is(err, approvalchain.ErrUnknownChain),
		errors.Is(err, approvalchain.ErrNoEngagedDesks):
		return fmt.Errorf("%w: %s", ErrInvalidArgument, err.Error())
	case errors.Is(err, approvalchain.ErrNotWaiting),
		errors.Is(err, approvalchain.ErrWrongDesk):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	default:
		return err
	}
}
