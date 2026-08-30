package repo

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/migrate"
)

// testProcurement gives a migrated repo against TEST_DATABASE_URL, matching the
// convention in budget_lifecycle_test.go. Skips without it, like every other DB
// test in this package.
func testProcurement(t *testing.T) (*Procurement, context.Context) {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run database tests")
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
	return NewProcurement(pool), ctx
}

// Migration 030: the caller's extension bag survives create, patch and both
// read paths.
//
// The bag exists because a catalogue client holds more about an item than
// procurement needs — which GL account a sale posts to, what it sells for,
// whether the line is still offered — and every one of those was being dropped
// on save with a 200. Asserted through the list query rather than the write's
// return value, because a column that persists but is missing from a SELECT is
// indistinguishable from one that was never stored.
func TestItemAttrsRoundTrip(t *testing.T) {
	p, ctx := testProcurement(t)
	sku := "SKU-" + uuid.NewString()[:8]

	bag := json.RawMessage(`{"salesAccount":"4010","salesPrice":"12500","purchaseAccount":"5010","status":"Active"}`)
	created, err := p.CreateItem(ctx, sku, "Cupping spoons", "Consumables", "ea", 0, 0, 3, "UGX", "", bag, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertBagHas(t, "create", created.Attrs, "salesAccount", "4010")

	items, err := p.ListItems(ctx, 200, 0, sku)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, it := range items {
		if it.SKU != sku {
			continue
		}
		found = true
		assertBagHas(t, "list", it.Attrs, "salesPrice", "12500")
		// The spelling that was silently dropping the price in both directions.
		if it.LastPrice != 3 {
			t.Errorf("lastPrice = %v, want 3", it.LastPrice)
		}
	}
	if !found {
		t.Fatal("the item is not in the list it was written to")
	}

	// A patch naming a bag replaces it; one that does not leaves it alone.
	newName := "Cupping spoons (steel)"
	patched, err := p.UpdateItem(ctx, created.ID, nil, &newName, nil, nil, nil, nil, nil, nil, nil, nil, "tester")
	if err != nil {
		t.Fatalf("patch without attrs: %v", err)
	}
	assertBagHas(t, "patch without attrs", patched.Attrs, "salesAccount", "4010")

	replacement := json.RawMessage(`{"salesAccount":"4020"}`)
	patched, err = p.UpdateItem(ctx, created.ID, nil, nil, nil, nil, nil, nil, nil, nil, nil, replacement, "tester")
	if err != nil {
		t.Fatalf("patch with attrs: %v", err)
	}
	assertBagHas(t, "patch with attrs", patched.Attrs, "salesAccount", "4020")
	var after map[string]any
	if err := json.Unmarshal(patched.Attrs, &after); err != nil {
		t.Fatalf("attrs not JSON: %v", err)
	}
	if _, still := after["salesPrice"]; still {
		t.Error("the replacement bag was merged, not replaced — a key the caller removed came back")
	}
}

// An item created with no bag must still write, and must read back as an empty
// object rather than as invalid JSON.
func TestItemWithoutAttrsStillWrites(t *testing.T) {
	p, ctx := testProcurement(t)
	sku := "SKU-" + uuid.NewString()[:8]
	created, err := p.CreateItem(ctx, sku, "Plain item", "", "ea", 0, 0, 0, "UGX", "", nil, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	items, err := p.ListItems(ctx, 200, 0, sku)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range items {
		if it.ID != created.ID {
			continue
		}
		if len(it.Attrs) == 0 {
			return // NULL-ish is fine; the column defaults to '{}'
		}
		var bag map[string]any
		if err := json.Unmarshal(it.Attrs, &bag); err != nil {
			t.Fatalf("attrs came back as invalid JSON: %q", it.Attrs)
		}
		if len(bag) != 0 {
			t.Errorf("attrs invented content: %s", it.Attrs)
		}
		return
	}
	t.Fatal("the item is not in the list")
}

func assertBagHas(t *testing.T, where string, raw json.RawMessage, key, want string) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("%s: attrs came back empty", where)
	}
	var bag map[string]any
	if err := json.Unmarshal(raw, &bag); err != nil {
		t.Fatalf("%s: attrs are not valid JSON: %v", where, err)
	}
	if got, _ := bag[key].(string); got != want {
		t.Errorf("%s: attrs.%s = %q, want %q", where, key, got, want)
	}
}
