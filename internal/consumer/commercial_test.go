package consumer

import "testing"

func TestMapPMUrgency(t *testing.T) {
	if mapPMUrgency("high") != "High" || mapPMUrgency("low") != "Low" || mapPMUrgency("") != "Medium" {
		t.Fatal("urgency mapping")
	}
}

func TestMapPMStatus(t *testing.T) {
	if mapPMStatus("approved") != "Approved" || mapPMStatus("draft") != "Draft" || mapPMStatus("submitted") != "Pending Approval" {
		t.Fatal("status mapping")
	}
}
