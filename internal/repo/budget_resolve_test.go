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

// An imported request is charged to its department's budget, else the
// configured default, else it is refused with ErrNoBudget — never the old
// "BDG-2026-UT", which stopped being a valid budget id in 027.
func TestResolveBudgetForDept(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the budget resolution test")
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
	dept := "Maint-" + uuid.NewString()[:8]
	var budgetID, defaultID string
	if err := pool.QueryRow(ctx, `INSERT INTO budgets (code, period, allocated, dept) VALUES ($1, '2026', 1000000, $2) RETURNING id::text`, "BDG-"+dept, dept).Scan(&budgetID); err != nil {
		t.Fatalf("budget: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO budgets (code, period, allocated, dept) VALUES ($1, '2026', 1, 'zz-default-test') RETURNING id::text`, "DEF-"+dept).Scan(&defaultID); err != nil {
		t.Fatalf("default budget: %v", err)
	}

	got, err := p.ResolveBudgetForDept(ctx, strings.ToLower(dept))
	if err != nil || got != budgetID {
		t.Fatalf("matched dept: %q %v, want %q", got, err, budgetID)
	}

	t.Setenv("PROCUREMENT_DEFAULT_BUDGET_ID", "")
	if _, err := p.ResolveBudgetForDept(ctx, "no-such-dept-"+dept); !errors.Is(err, ErrNoBudget) {
		t.Fatalf("no match, no default: %v", err)
	}
	t.Setenv("PROCUREMENT_DEFAULT_BUDGET_ID", defaultID)
	if got, err := p.ResolveBudgetForDept(ctx, "no-such-dept-"+dept); err != nil || got != defaultID {
		t.Fatalf("default: %q %v", got, err)
	}
	// A default that names no budget is no default.
	t.Setenv("PROCUREMENT_DEFAULT_BUDGET_ID", uuid.NewString())
	if _, err := p.ResolveBudgetForDept(ctx, "no-such-dept-"+dept); !errors.Is(err, ErrNoBudget) {
		t.Fatalf("missing default: %v", err)
	}

	// The PM import refuses an empty budget instead of inventing one.
	if _, err := p.ImportPMRequisition(ctx, "pm-"+uuid.NewString(), "", "Spanners", "Maint", "x@y", "Medium", "", 100, "UGX", "", "", "", "evt"); !errors.Is(err, ErrNoBudget) {
		t.Fatalf("PM import with no budget: %v", err)
	}
}
