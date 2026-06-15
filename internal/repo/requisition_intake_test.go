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

// TestImportProcurementRequest verifies generic inbound requests become
// "Pending Approval" requisitions and are idempotent on (origin, ref).
// Requires TEST_DATABASE_URL; skipped otherwise.
func TestImportProcurementRequest(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the procurement request intake test")
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

	origin := "iag-fleet"
	ref := "FLEET-" + uuid.NewString()

	row, err := p.ImportProcurementRequest(ctx, origin, ref, "Replace truck tyres", "Fleet",
		"driver@x", "High", 1500, "UGX", "", "Worn beyond limit", "evt-"+uuid.NewString())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if row.Status != "Pending Approval" {
		t.Fatalf("want status 'Pending Approval', got %q", row.Status)
	}

	// Same (origin, ref) is a no-op.
	if _, err := p.ImportProcurementRequest(ctx, origin, ref, "Replace truck tyres", "Fleet",
		"driver@x", "High", 1500, "UGX", "", "dup", "evt-"+uuid.NewString()); !errors.Is(err, ErrProcurementRequestExists) {
		t.Fatalf("want ErrProcurementRequestExists on duplicate, got %v", err)
	}

	// Exactly one requisition for this origin/ref.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM requisitions WHERE origin_system=$1 AND origin_ref=$2`, origin, ref).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 requisition, got %d", n)
	}
}
