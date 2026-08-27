package repo

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/migrate"
)

// TestSeedLoadToleratesNullBudgetID pins the NULL-safety of Seed.Load.
//
// `budget_id` is nullable on both requisitions and purchase_orders, but the two
// queries here selected it bare and scanned it straight into a non-pointer
// string field. A single row with no budget therefore failed the whole load
// with "cannot scan NULL into *string".
//
// That is not a one-endpoint outage. Seed.Load backs loadCached, which twelve
// list handlers share — including /budgets, which has no paginated fast path —
// so one unbudgeted purchase order took out vendors, items, requisitions, POs,
// GRNs and budgets at once. It also broke requisition *creation* downstream: a
// client that looks up a fallback budget got a 500 from /budgets and concluded
// no budget existed.
//
// Requires a Postgres via TEST_DATABASE_URL; skipped otherwise.
func TestSeedLoadToleratesNullBudgetID(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the seed NULL-budget integration test")
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
	actor := "actor-" + uuid.NewString()

	vendor, err := p.CreateVendor(ctx, "Vendor "+uuid.NewString(), "", "Supplies", "", "", "", "UG", "NET30", 0, "Active", actor)
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}

	// A requisition and a purchase order with no budget attached — the exact
	// shape that used to poison the payload. Inserted directly because the
	// write paths require a budget; the rows still exist in deployed data.
	reqID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO requisitions (id, title, dept, requester, priority, status, total, currency, budget_id)
		VALUES ($1, 'No-budget requisition', 'Ops', $2, 'Normal', 'Draft', 10, 'USD', NULL)`,
		reqID, actor); err != nil {
		t.Fatalf("insert requisition: %v", err)
	}
	poID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO purchase_orders (id, vendor_id, title, total, currency, status, budget_id)
		VALUES ($1, $2, 'No-budget PO', 25, 'USD', 'Draft', NULL)`,
		poID, vendor.ID); err != nil {
		t.Fatalf("insert purchase order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM purchase_orders WHERE id = $1`, poID)
		_, _ = pool.Exec(ctx, `DELETE FROM requisitions WHERE id = $1`, reqID)
	})

	data, err := NewSeed(pool).Load(ctx)
	if err != nil {
		t.Fatalf("Seed.Load with a NULL budget_id: %v", err)
	}

	// The load must succeed *and* still carry the rows, with the absent budget
	// reported as empty rather than dropped.
	var sawReq, sawPO bool
	for _, r := range data.Requisitions {
		if r.ID == reqID {
			sawReq = true
			if r.BudgetID != "" {
				t.Errorf("requisition budgetId = %q, want empty", r.BudgetID)
			}
		}
	}
	for _, po := range data.Pos {
		if po.ID == poID {
			sawPO = true
			if po.BudgetID != "" {
				t.Errorf("purchase order budgetId = %q, want empty", po.BudgetID)
			}
		}
	}
	if !sawReq {
		t.Error("requisition with NULL budget_id missing from the seed payload")
	}
	if !sawPO {
		t.Error("purchase order with NULL budget_id missing from the seed payload")
	}
}
