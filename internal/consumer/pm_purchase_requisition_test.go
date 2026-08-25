package consumer

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The producer is in another repo, so nothing at compile time connects this
// struct to what iag-project-management actually publishes. This fixture is
// copied verbatim from the emit in its
// `handlers/procurement_rest.go:submitProcurementPurchaseReq` — if the two
// drift, material requests silently stop reaching the approval ladder and the
// only symptom is that nothing appears in procurement.
const pmPurchaseRequisitionEvent = `{
  "id": "evt-7781",
  "type": "pm.purchase_requisition.submitted",
  "source": "iag-project-management",
  "data": {
    "purchaseRequisitionId": "PR-2026-0001",
    "workspaceOwnerUserId": "user-42",
    "title": "Cement, 42.5N",
    "total": "1250000.00",
    "currency": "UGX",
    "status": "Pending Approval",
    "requester": "asiimwe",
    "dept": "Civils",
    "priority": "High"
  }
}`

func TestPMPurchaseRequisitionEventDecodes(t *testing.T) {
	var evt PlatformEvent
	if err := json.Unmarshal([]byte(pmPurchaseRequisitionEvent), &evt); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if evt.Type != pmPurchaseRequisitionSubmitted {
		t.Fatalf("type = %q, want %q", evt.Type, pmPurchaseRequisitionSubmitted)
	}

	var data pmPurchaseRequisitionData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		t.Fatalf("payload: %v", err)
	}

	checks := []struct{ field, got, want string }{
		{"PurchaseRequisitionID", data.PurchaseRequisitionID, "PR-2026-0001"},
		{"WorkspaceOwnerUserID", data.WorkspaceOwnerUserID, "user-42"},
		{"Title", data.Title, "Cement, 42.5N"},
		{"Total", data.Total, "1250000.00"},
		{"Currency", data.Currency, "UGX"},
		{"Status", data.Status, "Pending Approval"},
		{"Requester", data.Requester, "asiimwe"},
		{"Dept", data.Dept, "Civils"},
		{"Priority", data.Priority, "High"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q — the producer's key has moved", c.field, c.got, c.want)
		}
	}
}

// The amount arrives as a formatted string, not a number: the producer sends
// fmt.Sprintf("%.2f", total). Parsing it is what decides whether the money desks
// have a band to work with.
func TestPMPurchaseRequisitionTotalParses(t *testing.T) {
	var evt PlatformEvent
	_ = json.Unmarshal([]byte(pmPurchaseRequisitionEvent), &evt)
	var data pmPurchaseRequisitionData
	_ = json.Unmarshal(evt.Data, &data)

	total, err := strconv.ParseFloat(strings.TrimSpace(data.Total), 64)
	if err != nil {
		t.Fatalf("total %q did not parse: %v", data.Total, err)
	}
	if total != 1_250_000 {
		t.Errorf("total = %v, want 1250000", total)
	}
}

// A material request has no payee and no urgency field — it has a priority.
// Mapping it through the same helpers as the cash requisition is the whole
// point of reusing the import, so the mappings have to hold for these values.
func TestPMPurchaseRequisitionMapsThroughTheSharedHelpers(t *testing.T) {
	if got := mapPMUrgency("High"); got != "High" {
		t.Errorf("priority High mapped to %q", got)
	}
	if got := mapPMUrgency(""); got != "Medium" {
		t.Errorf("a request with no priority mapped to %q, want Medium", got)
	}
	// The producer sets "Pending Approval" on submit; it must not be read as
	// approved, or the requisition would skip every desk.
	if got := mapPMStatus("Pending Approval"); got != "Pending Approval" {
		t.Errorf("submitted status mapped to %q", got)
	}
}

// Both requisition kinds land in the same table on the same unique column.
// They are only safe together because the id spaces cannot collide.
func TestRequisitionIDSpacesDoNotOverlap(t *testing.T) {
	cash := "12" // strconv.Itoa on an int id
	purchase := "PR-2026-0001"
	if cash == purchase {
		t.Fatal("ids collide")
	}
	if _, err := strconv.Atoi(purchase); err == nil {
		t.Error("a purchase requisition id parsed as an int; the two spaces can overlap")
	}
	if _, err := strconv.Atoi(cash); err != nil {
		t.Error("a cash requisition id is expected to be an int")
	}
}
