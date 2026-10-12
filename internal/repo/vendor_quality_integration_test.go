package repo

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"iag-procurement/backend/internal/models"
)

// Supplier quality status (033) against a real Postgres. Requires
// TEST_DATABASE_URL; skipped otherwise, like every DB test in this package.
func TestVendorQualityStatus(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p
	vendor, err := p.CreateVendor(ctx, "Kawacom "+uuid.NewString()[:8], "", "Coffee", "", "", "", "UG", "", 0, "Active", "buyer@iag")
	if err != nil {
		t.Fatal(err)
	}
	budget, err := p.CreateBudget(ctx, "BQ-"+uuid.NewString(), "FYTEST", 500_000_000, "Ops", "buyer@iag")
	if err != nil {
		t.Fatal(err)
	}
	item, err := p.CreateItem(ctx, "SKU-"+uuid.NewString()[:8], "Green coffee", "Coffee", "kg", 0, 0, 10, "UGX", "", nil, "buyer@iag")
	if err != nil {
		t.Fatal(err)
	}
	order := func() error {
		_, err := p.CreatePurchaseOrder(ctx, vendor.ID, "Green coffee", "UGX", budget.ID, "", nil,
			[]models.PoLine{{ItemID: item.ID, Qty: 10, Price: 10}}, "buyer@iag")
		return err
	}
	find := func() models.Vendor {
		t.Helper()
		list, err := p.ListVendors(ctx, 50, 0, vendor.Name)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %+v %v", list, err)
		}
		return list[0]
	}

	t.Run("quality advice is stored beside the decision, never as it", func(t *testing.T) {
		rate := 80.0
		now := time.Now().UTC()
		ok, err := p.ApplyQCRecommendation(ctx, QCRecommendation{SupplierRef: vendor.ID, RecommendedStatus: "suspended",
			AcceptanceRate: &rate, Reasons: []string{"80.0% of 10 lots accepted"}, At: now})
		if err != nil || !ok {
			t.Fatalf("apply: %v %v", ok, err)
		}
		v := find()
		if v.QCRecommendation != "suspended" || v.QualityStatus != "" || len(v.QCReasons) != 1 || v.QCAcceptanceRate == nil {
			t.Fatalf("advice: %+v", v)
		}
		if err := order(); err != nil {
			t.Fatalf("advice alone suspends nobody: %v", err)
		}
		// An older event cannot roll the advice back.
		if _, err := p.ApplyQCRecommendation(ctx, QCRecommendation{SupplierRef: vendor.ID, RecommendedStatus: "approved",
			At: now.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if v := find(); v.QCRecommendation != "suspended" {
			t.Fatalf("stale advice applied: %+v", v)
		}
		if ok, _ := p.ApplyQCRecommendation(ctx, QCRecommendation{SupplierRef: uuid.NewString(), RecommendedStatus: "approved", At: now}); ok {
			t.Fatal("an unknown supplier matches nothing")
		}
	})

	t.Run("procurement's suspension stops new orders until lifted", func(t *testing.T) {
		if err := p.SetVendorQualityStatus(ctx, vendor.ID, "suspended", "", "buyer@iag"); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("no reason: %v", err)
		}
		if err := p.SetVendorQualityStatus(ctx, vendor.ID, "blocked", "x", "buyer@iag"); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad status: %v", err)
		}
		if err := p.SetVendorQualityStatus(ctx, vendor.ID, "suspended", "three wet lots in a row", "buyer@iag"); err != nil {
			t.Fatal(err)
		}
		if v := find(); v.QualityStatus != "suspended" || v.QualityStatusReason != "three wet lots in a row" {
			t.Fatalf("decision: %+v", v)
		}
		if err := order(); !errors.Is(err, ErrSupplierSuspended) || !errors.Is(err, ErrConflict) {
			t.Fatalf("order to a suspended supplier: %v", err)
		}
		if err := p.SetVendorQualityStatus(ctx, vendor.ID, "conditional", "SCAR closed", "buyer@iag"); err != nil {
			t.Fatal(err)
		}
		if err := order(); err != nil {
			t.Fatalf("lifted: %v", err)
		}
		if err := p.SetVendorQualityStatus(ctx, uuid.NewString(), "approved", "x", "buyer@iag"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown vendor: %v", err)
		}
	})
}
