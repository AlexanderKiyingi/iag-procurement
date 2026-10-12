package consumer

import "testing"

func TestParseScorecard(t *testing.T) {
	raw := []byte(`{"id":"e1","type":"qc.supplier.scorecard","time":"2026-10-13T08:00:00Z","source":"iag.quality-control",
		"data":{"supplier_ref":"7b1c","recommended_status":"conditional","acceptance_rate":90.9,"reasons":["90.9% of 22 lots accepted"]}}`)
	_, rec, ok, err := parseScorecard(raw)
	if err != nil || !ok || rec.SupplierRef != "7b1c" || rec.RecommendedStatus != "conditional" ||
		rec.AcceptanceRate == nil || *rec.AcceptanceRate != 90.9 || len(rec.Reasons) != 1 || rec.At.Hour() != 8 {
		t.Fatalf("parse: %+v %v %v", rec, ok, err)
	}
	if _, _, ok, _ := parseScorecard([]byte(`{"type":"qc.batch.held","data":{}}`)); ok {
		t.Fatal("other events are not scorecards")
	}
	if _, _, ok, _ := parseScorecard([]byte(`{"type":"qc.supplier.scorecard","data":{"recommended_status":"approved"}}`)); ok {
		t.Fatal("a scorecard with no supplier is unusable")
	}
	if _, _, _, err := parseScorecard([]byte(`not json`)); err == nil {
		t.Fatal("junk is an error")
	}
}
