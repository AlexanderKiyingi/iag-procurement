package repo

import (
	"errors"
	"strings"
	"testing"
)

// The QC hold and the variance gate are pure functions on the row's values,
// so their truth tables are checked here without a database. The integration
// tests in wiring_remediation_integration_test.go prove the repo calls them
// in the right place.

func TestGrnQCGate(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		critical bool
		qc       string
		wantErr  bool
	}{
		{"draft, critical, no qc", "Draft", true, "", false},
		{"posted, not critical", "Posted", false, "", false},
		{"posted, critical, released", "Posted", true, "Released", false},
		{"posted, critical, released any case", "posted", true, " released ", false},
		{"posted, critical, no qc", "Posted", true, "", true},
		{"posted, critical, pending qc", "POSTED", true, "Pending", true},
		{"posted, critical, rejected qc", "Posted", true, "Rejected", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := grnQCGate(tc.status, tc.critical, tc.qc)
			if tc.wantErr {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("want ErrConflict, got %v", err)
				}
				if !strings.Contains(err.Error(), "quality-critical receipt cannot be posted until QC status is Released") {
					t.Fatalf("message = %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}

func TestInvoiceVarianceGate(t *testing.T) {
	// The fixture the spec names: PO 100, invoice 112, default 2% tolerance.
	err := invoiceVarianceGate(112, 100, 2.0, "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("12%% over with no resolution: want ErrConflict, got %v", err)
	}
	want := "invoice differs from purchase order by 12.0% (tolerance 2%); record a variance resolution before approving"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("message = %q, want it to contain %q", err.Error(), want)
	}

	if err := invoiceVarianceGate(112, 100, 2.0, "freight surcharge agreed"); err != nil {
		t.Fatalf("a recorded resolution must clear the gate: %v", err)
	}
	if err := invoiceVarianceGate(101.5, 100, 2.0, ""); err != nil {
		t.Fatalf("1.5%% is inside the tolerance: %v", err)
	}
	if err := invoiceVarianceGate(88, 100, 2.0, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("an under-billing is a variance too: %v", err)
	}
	if err := invoiceVarianceGate(112, 100, 15, ""); err != nil {
		t.Fatalf("a wider configured tolerance must be honoured: %v", err)
	}
	if err := invoiceVarianceGate(50, 0, 2.0, ""); err != nil {
		t.Fatalf("a zero PO total has no percentage; leave it to the match: %v", err)
	}
}
