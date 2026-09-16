package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/repo"
)

// These guards fire before any repo call, so a repo with no pool is enough:
// the handler must answer without ever touching it.
func statusTestAPI() *API {
	gin.SetMode(gin.TestMode)
	return &API{procurement: repo.NewProcurement(nil)}
}

func doPatch(t *testing.T, r *gin.Engine, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestPatchInvoiceRefusesApprovalByStatus(t *testing.T) {
	a := statusTestAPI()
	r := gin.New()
	r.PATCH("/invoices/:id", a.patchInvoice)

	for _, st := range []string{"approved", "Approved", " PAID ", "paid"} {
		code, out := doPatch(t, r, "/invoices/inv-1", `{"status":"`+st+`"}`)
		if code != http.StatusConflict {
			t.Fatalf("status %q: code = %d, want 409 (body %v)", st, code, out)
		}
		if out["error"] != invoiceStatusViaApprovalMsg {
			t.Fatalf("status %q: error = %v", st, out["error"])
		}
	}

	code, out := doPatch(t, r, "/invoices/inv-1", `{"status":"matched"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown status: code = %d, want 400 (body %v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.HasPrefix(msg, "invalid status; allowed: pending, approved, paid, disputed, rejected, cancelled") {
		t.Fatalf("unknown status: error = %q", msg)
	}
}

// matchStatus is no longer a field on the PATCH body. The struct is the
// contract, so this is asserted on the type rather than by a request that
// would need a database to be observed.
func TestPatchInvoiceBodyHasNoMatchStatus(t *testing.T) {
	raw, _ := json.Marshal(patchInvoiceBody{})
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["matchStatus"]; ok {
		t.Fatal("patchInvoiceBody still exposes matchStatus; the match is derived by the service")
	}
	for _, want := range []string{"varianceResolution", "dueDate", "status", "invoiceDate"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("patchInvoiceBody is missing %q", want)
		}
	}
}

func TestPatchRfqRejectsUnknownStatus(t *testing.T) {
	a := statusTestAPI()
	r := gin.New()
	r.PATCH("/rfqs/:id", a.patchRfq)

	code, out := doPatch(t, r, "/rfqs/rfq-1", `{"status":"pending"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body %v)", code, out)
	}
	if out["error"] != "invalid status; allowed: open, closed, awarded, cancelled" {
		t.Fatalf("error = %v", out["error"])
	}
	for _, st := range []string{"open", "closed", "awarded", "cancelled", "Open", " Closed "} {
		if !validRfqStatuses[strings.ToLower(strings.TrimSpace(st))] {
			t.Errorf("%q should be an allowed RFQ status", st)
		}
	}
}
