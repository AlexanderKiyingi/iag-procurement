package repo

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alvor-technologies/iag-platform-go/approvalchain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/migrate"
)

// Everything else about the desk chain is verified without a database: the
// engine by unit tests, the shipped matrix by parsing migration 020. These
// tests cover what only a real Postgres can answer — that the DDL executes,
// that TEXT[] columns round-trip through pgx, that the CHECK constraint fires,
// and that a transition commits its budget, outbox and audit side effects
// together with the state change.
//
// Requires TEST_DATABASE_URL; skipped otherwise, like the tiered approval test.

func deskTestPool(t *testing.T) (context.Context, *deskFixture) {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the desk chain integration tests")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, &deskFixture{t: t, p: NewProcurement(pool), pool: pool}
}

type deskFixture struct {
	t    *testing.T
	p    *Procurement
	pool *pgxpool.Pool
}

// scan reads a single value, failing the test on error.
func (f *deskFixture) scan(ctx context.Context, dest any, sql string, args ...any) error {
	f.t.Helper()
	return f.pool.QueryRow(ctx, sql, args...).Scan(dest)
}

func (f *deskFixture) exec(ctx context.Context, sql string, args ...any) error {
	f.t.Helper()
	_, err := f.pool.Exec(ctx, sql, args...)
	return err
}

// newRequisition creates a budget and a requisition of the given total.
func (f *deskFixture) newRequisition(ctx context.Context, total float64) (reqID, budgetID, requester string) {
	f.t.Helper()
	requester = "requester-" + uuid.NewString()
	budget, err := f.p.CreateBudget(ctx, "BC-"+uuid.NewString(), "FYTEST", 500_000_000, "Ops", requester)
	if err != nil {
		f.t.Fatalf("budget: %v", err)
	}
	req, err := f.p.CreateRequisition(
		ctx, "Desk chain test", "Ops", requester, "Medium", "", nil, total, "UGX", budget.ID, requester)
	if err != nil {
		f.t.Fatalf("requisition: %v", err)
	}
	return req.ID, budget.ID, requester
}

func deskActorFor(role string) approvalchain.Actor {
	return approvalchain.ActorWithRole(strings.ToLower(role)+"-"+uuid.NewString(), role)
}

func advance(t *testing.T, p *Procurement, ctx context.Context, id string, a approvalchain.Actor) approvalchain.State {
	t.Helper()
	s, err := p.ApplyDeskTransition(ctx, id, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Advance(st, a, "")
	})
	if err != nil {
		t.Fatalf("advance as %v: %v", a.Roles, err)
	}
	return s
}

// TestDeskChainWalksAndCommitsSideEffects is the core round-trip: open, submit,
// walk every desk, and confirm the requisition status, budget encumbrance,
// outbox event and audit rows all landed.
func TestDeskChainWalksAndCommitsSideEffects(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	// 25M engages every desk: PM, Accounts, GM, CEO, Finance.
	reqID, budgetID, requester := f.newRequisition(ctx, 25_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")

	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}

	// A second open is refused — one chain per requisition.
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("double open: want ErrConflict, got %v", err)
	}

	s, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if s.Desk != "pm" {
		t.Fatalf("first desk = %q, want pm", s.Desk)
	}

	for _, role := range []string{"Project Manager", "Accounts Assistant", "General Manager", "CEO"} {
		s = advance(t, p, ctx, reqID, deskActorFor(role))
		if s.Status != approvalchain.StatusInFlight {
			t.Fatalf("chain closed early after %s", role)
		}
	}
	s = advance(t, p, ctx, reqID, deskActorFor("Finance Officer"))
	if s.Status != approvalchain.StatusApproved {
		t.Fatalf("status = %s, want approved", s.Status)
	}

	// History survived the round trip in order: submit + five advances.
	reloaded, err := p.LoadDeskState(ctx, reqID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.History) != 6 {
		t.Fatalf("history has %d steps after reload, want 6", len(reloaded.History))
	}
	if reloaded.History[0].Action != approvalchain.ActionSubmit {
		t.Fatalf("first step = %q, want submit", reloaded.History[0].Action)
	}
	if reloaded.Status != approvalchain.StatusApproved || reloaded.Amount != 25_000_000 {
		t.Fatalf("reloaded state = %s / %.0f, want approved / 25000000", reloaded.Status, reloaded.Amount)
	}

	// The requisition mirrors the chain using the vocabulary the rest of the
	// service already reads.
	var status string
	if err := f.scan(ctx, &status, `SELECT status FROM requisitions WHERE id = $1`, reqID); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.EqualFold(status, "approved") {
		t.Fatalf("requisition status = %q, want approved", status)
	}

	// Budget encumbered exactly once — the defect that made desk approvals
	// spend without reserving.
	var pre float64
	if err := f.scan(ctx, &pre, `SELECT pre_committed FROM budgets WHERE id = $1`, budgetID); err != nil {
		t.Fatalf("read budget: %v", err)
	}
	if pre != 25_000_000 {
		t.Fatalf("pre_committed = %.0f, want 25000000", pre)
	}

	// The outcome event is in the outbox, committed with the approval rather
	// than fired-and-forgotten afterwards.
	var outboxRows int
	if err := f.scan(ctx, &outboxRows,
		`SELECT COUNT(*) FROM procurement_event_outbox WHERE event_key = $1 OR payload->>'id' = $1`, reqID); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxRows == 0 {
		t.Error("no outbox row for the approved requisition — the outcome event was not committed with it")
	}

	// Every transition left an audit row.
	var auditRows int
	if err := f.scan(ctx, &auditRows, `SELECT COUNT(*) FROM audit_entries WHERE target = $1`, reqID); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditRows < 6 {
		t.Errorf("audit rows = %d, want one per transition (>=6)", auditRows)
	}
}

// TestDeskChainRebaseClosesTheBandBypassAgainstTheDatabase is the regression
// test for the worst defect found in review: a requisition opened below a band
// and then edited above it must not finish on the small-request desks.
func TestDeskChainRebaseClosesTheBandBypassAgainstTheDatabase(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	// Opened at 1M: GM and CEO never engage.
	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	s := advance(t, p, ctx, reqID, deskActorFor("Project Manager"))
	if s.Desk != "accounts" {
		t.Fatalf("desk = %q, want accounts", s.Desk)
	}

	// The requisition is raised to 50M while it sits on Accounts.
	if err := f.exec(ctx, `UPDATE requisitions SET total = 50000000 WHERE id = $1`, reqID); err != nil {
		t.Fatalf("raise total: %v", err)
	}

	// The next transition must re-evaluate against the live total and pull GM
	// in. Before the fix this went straight to Finance and paid 50M on two
	// signatures.
	s = advance(t, p, ctx, reqID, deskActorFor("Accounts Assistant"))
	if s.Desk != "gm" {
		t.Fatalf("desk after accounts = %q, want gm — the raised total must re-engage GM", s.Desk)
	}
	if s.Amount != 50_000_000 {
		t.Fatalf("state amount = %.0f, want the live 50000000", s.Amount)
	}
	s = advance(t, p, ctx, reqID, deskActorFor("General Manager"))
	if s.Desk != "ceo" {
		t.Fatalf("desk after gm = %q, want ceo", s.Desk)
	}
}

// TestDeskChainRejectionRequiresAReasonAtEveryLayer proves the reason is
// enforced by the engine and, independently, by the database CHECK constraint —
// so no future code path can write a silent refusal.
func TestDeskChainRejectionRequiresAReasonAtEveryLayer(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, budgetID, requester := f.newRequisition(ctx, 1_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Engine layer.
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Reject(st, deskActorFor("Project Manager"), "  ")
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank reject reason: want ErrInvalidArgument, got %v", err)
	}

	// Database layer: the constraint must refuse it even with the engine bypassed.
	if err := f.exec(ctx, `
		INSERT INTO requisition_approval_steps (requisition_id, seq, desk, action, reason)
		VALUES ($1, 999, 'pm', 'reject', '')`, reqID); err == nil {
		t.Error("the reason_required CHECK constraint did not fire on a blank rejection")
	}

	s, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Reject(st, deskActorFor("Project Manager"), "no budget line")
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if s.Status != approvalchain.StatusRejected || s.LastReason() != "no budget line" {
		t.Fatalf("state = %s / %q, want rejected with the reason", s.Status, s.LastReason())
	}

	// A rejected requisition never encumbered anything.
	var pre float64
	if err := f.scan(ctx, &pre, `SELECT pre_committed FROM budgets WHERE id = $1`, budgetID); err != nil {
		t.Fatalf("read budget: %v", err)
	}
	if pre != 0 {
		t.Fatalf("pre_committed = %.0f after rejection, want 0", pre)
	}
}

// TestDeskChainAmendRestartsTheWalkAcrossAReload confirms the amend semantics
// survive persistence: the cursor resets and history is preserved.
func TestDeskChainAmendRestartsTheWalkAcrossAReload(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	advance(t, p, ctx, reqID, deskActorFor("Project Manager"))

	s, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Amend(st, deskActorFor("Accounts Assistant"), "attach the supplier quote")
	})
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if s.Status != approvalchain.StatusReturned {
		t.Fatalf("status = %s, want returned_for_amendment", s.Status)
	}

	s, err = p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	})
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if s.Desk != "pm" {
		t.Fatalf("resubmitted onto %q, want pm — an amended request is re-approved from the start", s.Desk)
	}

	reloaded, err := p.LoadDeskState(ctx, reqID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// submit, pm advance, amend, resubmit
	if len(reloaded.History) != 4 {
		t.Fatalf("history has %d steps, want 4 — amendment must not erase what happened", len(reloaded.History))
	}
}

// TestDeskQueueShowsOnlyWhatTheActorCanActOn covers the queue query plus the
// four-eyes filter applied on top of it.
func TestDeskQueueShowsOnlyWhatTheActorCanActOn(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	inQueue := func(a approvalchain.Actor) bool {
		items, err := p.DeskQueue(ctx, a)
		if err != nil {
			t.Fatalf("desk queue: %v", err)
		}
		for _, it := range items {
			if it.RequisitionID == reqID {
				return true
			}
		}
		return false
	}

	if !inQueue(deskActorFor("Project Manager")) {
		t.Error("the PM desk should see a request sitting on it")
	}
	if inQueue(deskActorFor("CEO")) {
		t.Error("the CEO desk should not see a request sitting on PM")
	}
	// A PM who raised the request cannot approve it, so it must not appear in
	// their queue either.
	if inQueue(approvalchain.ActorWithRole(requester, "Project Manager")) {
		t.Error("four-eyes: a requester must not see their own request as actionable")
	}
}

// TestReplaceDeskChainRefusesWhileRequestsAreWalkingIt guards the matrix edit
// path: re-cutting a chain under an in-flight request could strand it on a desk
// that no longer exists.
func TestReplaceDeskChainRefusesWhileRequestsAreWalkingIt(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	requesterActor := approvalchain.ActorWithRole(requester, "Clerk")
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
		return e.Submit(st, requesterActor)
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	rows, err := p.ListDeskMatrix(ctx)
	if err != nil {
		t.Fatalf("list matrix: %v", err)
	}
	var current []DeskRow
	for _, r := range rows {
		if r.ChainKey == ChainRequisition {
			current = append(current, r)
		}
	}
	if len(current) == 0 {
		t.Fatal("the requisition chain has no desks in the matrix")
	}

	if _, err := p.ReplaceDeskChain(ctx, ChainRequisition, current); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-cutting a chain under an in-flight request: want ErrConflict, got %v", err)
	}

	// An unroutable definition is refused on validation, before any write.
	bad := []DeskRow{{Desk: "gm", Label: "GM", RolePatterns: []string{`\bgm\b`}, MinAmount: 10}}
	if _, err := p.ReplaceDeskChain(ctx, "scratch-"+uuid.NewString(), bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("all-banded chain: want ErrInvalidArgument, got %v", err)
	}
}

// TestDeskChainScopesThePMDeskToTheAssignedManager proves scope is captured
// from the requisition at open and enforced on transition — the difference
// between "a Project Manager approves it" and "any Project Manager approves it".
func TestDeskChainScopesThePMDeskToTheAssignedManager(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	const owner = "alice@iag.local"
	if err := f.exec(ctx,
		`UPDATE requisitions SET pm_workspace_owner = $2 WHERE id = $1`, reqID, owner); err != nil {
		t.Fatalf("assign project owner: %v", err)
	}

	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	st, err := p.LoadDeskState(ctx, reqID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if st.Scope["project_owner"] != owner {
		t.Fatalf("scope project_owner = %q, want %q — ownership must be captured at open",
			st.Scope["project_owner"], owner)
	}

	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, s approvalchain.State) (approvalchain.State, error) {
		return e.Submit(s, approvalchain.ActorWithRole(requester, "Clerk"))
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// A PM who holds the role but not this project is refused.
	if _, err := p.ApplyDeskTransition(ctx, reqID, func(e *approvalchain.Engine, s approvalchain.State) (approvalchain.State, error) {
		return e.Advance(s, approvalchain.ActorWithRole("bob@iag.local", "Project Manager"), "")
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owning PM: want ErrForbidden, got %v", err)
	}

	// The assigned manager clears it.
	s := advance(t, p, ctx, reqID, approvalchain.ActorWithRole(owner, "Project Manager"))
	if s.Desk != "accounts" {
		t.Fatalf("desk = %q, want accounts", s.Desk)
	}

	// And the queue agrees with the transition rule.
	items, err := p.DeskQueue(ctx, approvalchain.ActorWithRole("bob@iag.local", "Project Manager"))
	if err != nil {
		t.Fatalf("desk queue: %v", err)
	}
	for _, it := range items {
		if it.RequisitionID == reqID {
			t.Error("a non-owning PM must not see the request in their queue either")
		}
	}
}

// TestOpenDeskChainRefusesATierApprovedRequisition keeps the two approval
// mechanisms from each holding half a requisition's story.
func TestOpenDeskChainRefusesATierApprovedRequisition(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	if _, _, err := p.ApproveRequisitionTier(
		ctx, reqID, "supervisor-"+uuid.NewString(),
		permSet("procurement.approve_requisition_tier1"), "ok"); err != nil {
		t.Fatalf("tier approve: %v", err)
	}
	if _, err := p.OpenDeskChain(ctx, reqID, ChainRequisition, requester, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("desk chain on a tier-approved requisition: want ErrConflict, got %v", err)
	}
}
