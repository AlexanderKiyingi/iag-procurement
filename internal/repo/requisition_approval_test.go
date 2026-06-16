package repo

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/migrate"
)

// permSet is a tiny stand-in for an approver's granted permissions, matching the
// signature ApproveRequisitionTier expects.
func permSet(codes ...string) func(string) bool {
	have := map[string]bool{}
	for _, c := range codes {
		have[c] = true
	}
	return func(code string) bool { return have[code] }
}

// TestTieredRequisitionApproval exercises the amount-band approval matrix: a
// requisition in the second band needs two distinct signatures, the requester
// cannot approve, no one can sign two tiers, and the second tier's permission is
// enforced. Requires TEST_DATABASE_URL (skipped otherwise).
func TestTieredRequisitionApproval(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the tiered approval integration test")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	if err := migrate.Up(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	p := NewProcurement(pool)

	const (
		tier1 = "procurement.approve_requisition_tier1"
		tier2 = "procurement.approve_requisition_tier2"
	)
	requester := "requester-" + uuid.NewString()
	supervisor := "supervisor-" + uuid.NewString()
	manager := "manager-" + uuid.NewString()

	budget, err := p.CreateBudget(ctx, "BC-"+uuid.NewString(), "FYTEST", 50_000_000, "Ops", manager)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}

	// 10,000,000 sits in band 2 -> requires tier1 + tier2 signatures.
	req, err := p.CreateRequisition(ctx, "Fuel", "Ops", requester, "Medium", "", nil, 10_000_000, "UGX", budget.ID, requester)
	if err != nil {
		t.Fatalf("requisition: %v", err)
	}

	// Requester cannot approve their own requisition.
	if _, _, err := p.ApproveRequisitionTier(ctx, req.ID, requester, permSet(tier1), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self-approval: want ErrForbidden, got %v", err)
	}

	// Supervisor clears tier 1; not yet complete.
	_, prog, err := p.ApproveRequisitionTier(ctx, req.ID, supervisor, permSet(tier1), "ok")
	if err != nil {
		t.Fatalf("tier1 approve: %v", err)
	}
	if prog.Complete {
		t.Fatalf("tier1 approve should not finalise a two-tier requisition")
	}
	if prog.NextTier == nil || *prog.NextTier != 2 {
		t.Fatalf("want next tier 2, got %+v", prog.NextTier)
	}

	// Same person cannot sign a second tier.
	if _, _, err := p.ApproveRequisitionTier(ctx, req.ID, supervisor, permSet(tier1, tier2), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("distinct approver: want ErrForbidden, got %v", err)
	}

	// A second approver lacking the tier-2 permission is refused.
	other := "other-" + uuid.NewString()
	if _, _, err := p.ApproveRequisitionTier(ctx, req.ID, other, permSet(tier1), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing tier2 perm: want ErrForbidden, got %v", err)
	}

	// Manager holds tier 2 -> finalises.
	row, prog, err := p.ApproveRequisitionTier(ctx, req.ID, manager, permSet(tier2), "approved")
	if err != nil {
		t.Fatalf("tier2 approve: %v", err)
	}
	if !prog.Complete || !strings.EqualFold(row.Status, "approved") {
		t.Fatalf("want complete+approved, got complete=%v status=%q", prog.Complete, row.Status)
	}

	// Budget pre-encumbered by the requisition total exactly once.
	var pre float64
	if err := pool.QueryRow(ctx, `SELECT pre_committed FROM budgets WHERE id = $1`, budget.ID).Scan(&pre); err != nil {
		t.Fatalf("read budget: %v", err)
	}
	if pre != 10_000_000 {
		t.Fatalf("want pre_committed=10000000, got %.0f", pre)
	}

	// Re-approving a finalised requisition is refused.
	if _, _, err := p.ApproveRequisitionTier(ctx, req.ID, "late-"+uuid.NewString(), permSet(tier1, tier2), ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("re-approve terminal: want ErrInvalidArgument, got %v", err)
	}
}
