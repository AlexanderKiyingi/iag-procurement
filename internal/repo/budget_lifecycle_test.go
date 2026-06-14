package repo

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/migrate"
	"iag-procurement/backend/internal/models"
)

// TestBudgetLifecycle exercises the three-stage encumbrance ledger end to end.
// It requires a Postgres reachable via TEST_DATABASE_URL (e.g. the docker-compose
// db: postgres://procurement:procurement@127.0.0.1:5432/procurement) and is
// skipped otherwise.
func TestBudgetLifecycle(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the budget lifecycle integration test")
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
	p.SetApprovalThreshold(1_000_000) // auto-approve POs so receipts are allowed

	requester := "requester-" + uuid.NewString()
	approver := "approver-" + uuid.NewString()

	vendor, err := p.CreateVendor(ctx, "Vendor "+uuid.NewString(), "", "Supplies", "", "", "", "UG", "NET30", 0, "Active", approver)
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}
	item, err := p.CreateItem(ctx, "SKU-"+uuid.NewString(), "Widget", "Supplies", "ea", 0, 0, 0, "USD", "", approver)
	if err != nil {
		t.Fatalf("item: %v", err)
	}
	budget, err := p.CreateBudget(ctx, "BC-"+uuid.NewString(), "FYTEST", 1000, "Ops", approver)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}

	readB := func() (pre, committed, spent, remaining float64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT pre_committed, committed, spent, remaining FROM budgets WHERE id = $1`, budget.ID).
			Scan(&pre, &committed, &spent, &remaining); err != nil {
			t.Fatalf("read budget: %v", err)
		}
		return
	}

	// 1) Approve a requisition for 600 -> pre-encumbered.
	req, err := p.CreateRequisition(ctx, "Req", "Ops", requester, "Medium", "", nil, 600, "USD", budget.ID, requester)
	if err != nil {
		t.Fatalf("requisition: %v", err)
	}
	approved := "approved"
	if _, err := p.UpdateRequisition(ctx, req.ID, nil, nil, nil, &approved, nil, nil, nil, nil, approver); err != nil {
		t.Fatalf("approve requisition: %v", err)
	}
	if pre, c, s, _ := readB(); pre != 600 || c != 0 || s != 0 {
		t.Fatalf("after approve: want pre=600 committed=0 spent=0, got pre=%.0f committed=%.0f spent=%.0f", pre, c, s)
	}

	// 2) Raise a PO for 600 against the requisition -> pre liquidated, firm committed.
	po, err := p.CreatePurchaseOrder(ctx, vendor.ID, "PO", "USD", budget.ID, req.ID, nil,
		[]models.PoLine{{ItemID: item.ID, Qty: 1, Price: 600}}, approver)
	if err != nil {
		t.Fatalf("po: %v", err)
	}
	if pre, c, s, _ := readB(); pre != 0 || c != 600 || s != 0 {
		t.Fatalf("after PO: want pre=0 committed=600 spent=0, got pre=%.0f committed=%.0f spent=%.0f", pre, c, s)
	}

	// 3) Partial receipt of 300 -> committed 300, spent 300.
	if _, err := p.CreateGrn(ctx, vendor.ID, &po.ID, approver, "Posted", nil,
		[]models.GrnLine{{ItemID: item.ID, Qty: 1, UnitPrice: 300}}, approver); err != nil {
		t.Fatalf("grn1: %v", err)
	}
	if pre, c, s, _ := readB(); pre != 0 || c != 300 || s != 300 {
		t.Fatalf("after partial GRN: want pre=0 committed=300 spent=300, got pre=%.0f committed=%.0f spent=%.0f", pre, c, s)
	}

	// 4) Remainder receipt of 300 -> committed 0, spent 600.
	if _, err := p.CreateGrn(ctx, vendor.ID, &po.ID, approver, "Posted", nil,
		[]models.GrnLine{{ItemID: item.ID, Qty: 1, UnitPrice: 300}}, approver); err != nil {
		t.Fatalf("grn2: %v", err)
	}
	if pre, c, s, rem := readB(); pre != 0 || c != 0 || s != 600 || rem != 400 {
		t.Fatalf("after full GRN: want pre=0 committed=0 spent=600 remaining=400, got pre=%.0f committed=%.0f spent=%.0f rem=%.0f", pre, c, s, rem)
	}

	// 5) Over-budget approval is refused (available is 400, request is 600).
	req2, err := p.CreateRequisition(ctx, "Req2", "Ops", requester, "Medium", "", nil, 600, "USD", budget.ID, requester)
	if err != nil {
		t.Fatalf("requisition2: %v", err)
	}
	if _, err := p.UpdateRequisition(ctx, req2.ID, nil, nil, nil, &approved, nil, nil, nil, nil, approver); err == nil {
		t.Fatalf("expected over-budget approval to be rejected")
	}

	// 6) Self-approval is refused.
	req3, err := p.CreateRequisition(ctx, "Req3", "Ops", requester, "Medium", "", nil, 100, "USD", budget.ID, requester)
	if err != nil {
		t.Fatalf("requisition3: %v", err)
	}
	if _, err := p.UpdateRequisition(ctx, req3.ID, nil, nil, nil, &approved, nil, nil, nil, nil, requester); err == nil {
		t.Fatalf("expected self-approval to be rejected")
	}

	// 7) Approve a 200 requisition (pre=200), then lapse the period -> pre/committed cleared.
	req4, err := p.CreateRequisition(ctx, "Req4", "Ops", requester, "Medium", "", nil, 200, "USD", budget.ID, requester)
	if err != nil {
		t.Fatalf("requisition4: %v", err)
	}
	if _, err := p.UpdateRequisition(ctx, req4.ID, nil, nil, nil, &approved, nil, nil, nil, nil, approver); err != nil {
		t.Fatalf("approve requisition4: %v", err)
	}
	if pre, _, _, _ := readB(); pre != 200 {
		t.Fatalf("after req4 approve: want pre=200, got %.0f", pre)
	}
	if _, err := p.CloseBudgetPeriod(ctx, "lapse", BudgetCloseFilter{BudgetID: budget.ID}, "system"); err != nil {
		t.Fatalf("close period: %v", err)
	}
	if pre, c, s, rem := readB(); pre != 0 || c != 0 || s != 600 || rem != 400 {
		t.Fatalf("after lapse: want pre=0 committed=0 spent=600 remaining=400, got pre=%.0f committed=%.0f spent=%.0f rem=%.0f", pre, c, s, rem)
	}
}
