package consumer

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFuelRequisitionTitle(t *testing.T) {
	cases := []struct {
		d    fuelRequestApprovedData
		want string
	}{
		{fuelRequestApprovedData{Litres: "120.00", VehicleID: "VH-1"}, "Fuel — 120.00L (VH-1)"},
		{fuelRequestApprovedData{VehicleID: "VH-2"}, "Fuel — VH-2"},
		{fuelRequestApprovedData{RequestID: "FREQ-9"}, "Fuel request FREQ-9"},
	}
	for _, c := range cases {
		if got := fuelRequisitionTitle(c.d); got != c.want {
			t.Errorf("fuelRequisitionTitle(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// Non-approved transitions and unrelated event types must be ignored before the
// repo is ever touched — verified here with a nil repo so any DB call panics.
func TestFleetHandleIgnoresNonSourcingEvents(t *testing.T) {
	c := &Fleet{repo: nil}
	ctx := context.Background()

	rejected, _ := json.Marshal(map[string]any{
		"type": fuelRequestApproved,
		"id":   "evt-1",
		"data": map[string]any{"requestId": "FREQ-1", "status": "rejected"},
	})
	if err := c.handle(ctx, rejected); err != nil {
		t.Fatalf("rejected fuel request: unexpected err %v", err)
	}

	other, _ := json.Marshal(map[string]any{
		"type": "fleet.fuel.recorded",
		"id":   "evt-2",
		"data": map[string]any{"amount": "100"},
	})
	if err := c.handle(ctx, other); err != nil {
		t.Fatalf("unrelated event: unexpected err %v", err)
	}
}

func TestFleetHandleDropsApprovedWithoutRequestID(t *testing.T) {
	c := &Fleet{repo: nil}
	approvedNoRef, _ := json.Marshal(map[string]any{
		"type": fuelRequestApproved,
		"id":   "evt-3",
		"data": map[string]any{"status": "approved"},
	})
	if err := c.handle(context.Background(), approvedNoRef); err != nil {
		t.Fatalf("approved-without-ref: unexpected err %v", err)
	}
}
