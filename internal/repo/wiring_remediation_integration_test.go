package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alvor-technologies/iag-platform-go/approvalchain"
	"github.com/google/uuid"

	"iag-procurement/backend/internal/models"
)

// Database-backed coverage for the wiring remediation: the self-opening desk
// submit, the QC hold on posting, the variance gate on invoice approval, and
// the migration-032 columns surviving every read path. Requires
// TEST_DATABASE_URL; skipped otherwise, like every DB test in this package.

// TestDeskSubmitOpensTheChainWhenNoStateExists: a client that never called
// /desk/open still lands on the first desk, and the state row now exists.
func TestDeskSubmitOpensTheChainWhenNoStateExists(t *testing.T) {
	ctx, f := deskTestPool(t)
	p := f.p

	reqID, _, requester := f.newRequisition(ctx, 1_000_000)
	if _, err := p.LoadDeskState(ctx, reqID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("precondition: want no desk state, got %v", err)
	}

	s, err := p.ApplyDeskTransitionSelfOpening(ctx, reqID, requester,
		func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
			return e.Submit(st, approvalchain.ActorWithRole(requester, "Clerk"))
		})
	if err != nil {
		t.Fatalf("self-opening submit: %v", err)
	}
	if s.Status != approvalchain.StatusInFlight || s.Desk != "pm" {
		t.Fatalf("state = %s / %q, want in_flight on pm", s.Status, s.Desk)
	}

	reloaded, err := p.LoadDeskState(ctx, reqID)
	if err != nil {
		t.Fatalf("the state row was not created: %v", err)
	}
	if reloaded.ChainKey != ChainRequisition || reloaded.Requester != requester {
		t.Fatalf("opened as %s / %q, want %s / %q", reloaded.ChainKey, reloaded.Requester, ChainRequisition, requester)
	}
	if len(reloaded.History) != 1 || reloaded.History[0].Action != approvalchain.ActionSubmit {
		t.Fatalf("history = %+v, want exactly one submit step", reloaded.History)
	}
	var status string
	if err := f.scan(ctx, &status, `SELECT status FROM requisitions WHERE id = $1`, reqID); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.EqualFold(status, "pending approval") {
		t.Fatalf("requisition status = %q, want pending approval", status)
	}

	// Already open: the self-opening variant behaves exactly like the plain one.
	if _, err := p.ApplyDeskTransitionSelfOpening(ctx, reqID, requester,
		func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
			return e.Submit(st, approvalchain.ActorWithRole(requester, "Clerk"))
		}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second submit on an in-flight chain: want ErrConflict, got %v", err)
	}
	// And the non-opening variant still 404s on a requisition with no state.
	other, _, _ := f.newRequisition(ctx, 1_000_000)
	if _, err := p.ApplyDeskTransition(ctx, other,
		func(e *approvalchain.Engine, st approvalchain.State) (approvalchain.State, error) {
			return e.Submit(st, approvalchain.ActorWithRole(requester, "Clerk"))
		}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("advance-style transition with no state: want ErrNotFound, got %v", err)
	}
}

// receivingFixture builds vendor, item, budget and an auto-approved PO of the
// given total so receipts and invoices can be raised against it.
func receivingFixture(t *testing.T, p *Procurement, ctx context.Context, poTotal float64) (vendorID, itemID, budgetID, poID string) {
	t.Helper()
	actor := "buyer-" + uuid.NewString()
	p.SetApprovalThreshold(1_000_000_000) // POs below this auto-approve, so they are receivable
	vendor, err := p.CreateVendor(ctx, "Vendor "+uuid.NewString(), "", "Supplies", "", "", "", "UG", "NET30", 0, "Active", actor)
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}
	item, err := p.CreateItem(ctx, "SKU-"+uuid.NewString(), "Widget", "Supplies", "ea", 0, 0, 0, "USD", "", nil, actor)
	if err != nil {
		t.Fatalf("item: %v", err)
	}
	budget, err := p.CreateBudget(ctx, "BC-"+uuid.NewString(), "FYTEST", 1_000_000, "Ops", actor)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}
	po, err := p.CreatePurchaseOrder(ctx, vendor.ID, "PO", "USD", budget.ID, "", nil,
		[]models.PoLine{{ItemID: item.ID, Qty: 1, Price: poTotal}}, actor)
	if err != nil {
		t.Fatalf("po: %v", err)
	}
	return vendor.ID, item.ID, budget.ID, po.ID
}

func TestGrnQCGateHoldsPostingAgainstTheDatabase(t *testing.T) {
	p, ctx := testProcurement(t)
	vendorID, itemID, _, poID := receivingFixture(t, p, ctx, 100)
	lines := []models.GrnLine{{ItemID: itemID, Qty: 1, UnitPrice: 100}}

	// Create: quality-critical + Posted + no release is refused.
	if _, err := p.CreateGrn(ctx, vendorID, &poID, "receiver", "Posted", nil, lines, true, "Pending", "Main", "", "tester"); !errors.Is(err, ErrConflict) {
		t.Fatalf("posting a held receipt on create: want ErrConflict, got %v", err)
	}

	// Draft is fine, and the new fields echo on create (default status Draft).
	g, err := p.CreateGrn(ctx, vendorID, &poID, "receiver", "", nil, lines, true, "Pending", "Main store", "fragile", "tester")
	if err != nil {
		t.Fatalf("draft create: %v", err)
	}
	if g.Status != "Draft" || !g.QualityCritical || g.QCStatus != "Pending" || g.Warehouse != "Main store" || g.Notes != "fragile" {
		t.Fatalf("create echo = %+v", g)
	}

	// Update to Posted while held is refused, and the tx rolled back.
	posted := "Posted"
	if _, err := p.UpdateGrn(ctx, g.ID, nil, nil, nil, nil, &posted, nil, nil, nil, nil, nil, "tester"); !errors.Is(err, ErrConflict) {
		t.Fatalf("posting a held receipt on update: want ErrConflict, got %v", err)
	}
	var st string
	if err := p.pool.QueryRow(ctx, `SELECT status FROM grns WHERE id = $1`, g.ID).Scan(&st); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st != "Draft" {
		t.Fatalf("status after refused post = %q, want Draft (the update must roll back)", st)
	}

	// Releasing QC and posting in one patch goes through.
	released := "Released"
	g2, err := p.UpdateGrn(ctx, g.ID, nil, nil, nil, nil, &posted, nil, nil, &released, nil, nil, "tester")
	if err != nil {
		t.Fatalf("post after release: %v", err)
	}
	if g2.Status != "Posted" || g2.QCStatus != "Released" || g2.Warehouse != "Main store" {
		t.Fatalf("update echo = %+v", g2)
	}

	// Every read path carries the columns.
	list, err := p.ListGrns(ctx, 500, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, row := range list {
		if row.ID == g.ID {
			found = true
			if !row.QualityCritical || row.QCStatus != "Released" || row.Warehouse != "Main store" || row.Notes != "fragile" {
				t.Errorf("list row = %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("GRN missing from the paged list")
	}
	seed, err := NewSeed(p.pool).Load(ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	found = false
	for _, row := range seed.Grns {
		if row.ID == g.ID {
			found = true
			if !row.QualityCritical || row.QCStatus != "Released" || row.Notes != "fragile" {
				t.Errorf("seed row = %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("GRN missing from the seed snapshot")
	}
}

func TestInvoiceApprovalVarianceGateAgainstTheDatabase(t *testing.T) {
	p, ctx := testProcurement(t)
	vendorID, itemID, _, poID := receivingFixture(t, p, ctx, 100)
	if _, err := p.CreateGrn(ctx, vendorID, &poID, "receiver", "Posted", nil,
		[]models.GrnLine{{ItemID: itemID, Qty: 1, UnitPrice: 100}}, false, "", "", "", "tester"); err != nil {
		t.Fatalf("grn: %v", err)
	}

	due := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	inv, err := p.CreateInvoice(ctx, vendorID, &poID, 112, "USD", nil, nil, "", &due, "tester")
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if inv.DueDate != "2026-10-01" || inv.VarianceResolution != "" {
		t.Fatalf("create echo = %+v", inv)
	}

	_, err = p.ApproveInvoice(ctx, inv.ID, "approver")
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "differs from purchase order by 12.0% (tolerance 2%)") {
		t.Fatalf("12%% over with no resolution: want the variance conflict, got %v", err)
	}

	// Recording a resolution clears the variance gate; the three-way match
	// still stands behind it (amounts disagree, so it stays Amount variance).
	res := "freight surcharge agreed with vendor"
	patched, err := p.UpdateInvoice(ctx, inv.ID, nil, nil, nil, nil, nil, nil, nil, nil, &res, nil, "tester")
	if err != nil {
		t.Fatalf("record resolution: %v", err)
	}
	if patched.VarianceResolution != res || patched.DueDate != "2026-10-01" {
		t.Fatalf("update echo = %+v", patched)
	}
	_, err = p.ApproveInvoice(ctx, inv.ID, "approver")
	if !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), "variance resolution") {
		t.Fatalf("with a resolution the variance gate must step aside for the match check, got %v", err)
	}

	// Inside the tolerance and matched: approves.
	ok, err := p.CreateInvoice(ctx, vendorID, &poID, 100, "USD", nil, nil, "", nil, "tester")
	if err != nil {
		t.Fatalf("matched invoice: %v", err)
	}
	approved, err := p.ApproveInvoice(ctx, ok.ID, "approver")
	if err != nil {
		t.Fatalf("approve matched invoice: %v", err)
	}
	if approved.Status != "Approved" || approved.DueDate != "" {
		t.Fatalf("approved = %+v", approved)
	}

	// dueDate present-and-nil clears.
	var nilTime *time.Time
	cleared, err := p.UpdateInvoice(ctx, inv.ID, nil, nil, nil, nil, nil, nil, nil, nil, nil, &nilTime, "tester")
	if err != nil {
		t.Fatalf("clear due date: %v", err)
	}
	if cleared.DueDate != "" {
		t.Fatalf("dueDate = %q after clearing", cleared.DueDate)
	}

	// The paged list carries both columns.
	list, err := p.ListInvoices(ctx, 500, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, row := range list {
		if row.ID == inv.ID && row.VarianceResolution != res {
			t.Errorf("list row lost the resolution: %+v", row)
		}
	}
}

// TestFormFieldsRoundTripOnRequisitionContractAndLinks covers the remaining
// migration-032 columns plus the requisitionId link on PO and RFQ reads.
func TestFormFieldsRoundTripOnRequisitionContractAndLinks(t *testing.T) {
	p, ctx := testProcurement(t)
	actor := "tester-" + uuid.NewString()
	vendorID, itemID, budgetID, _ := receivingFixture(t, p, ctx, 100)

	req, err := p.CreateRequisition(ctx, "Notes test", "Ops", actor, "Medium", "", nil, 50, "USD", budgetID, "urgent: line down", actor)
	if err != nil {
		t.Fatalf("requisition: %v", err)
	}
	if req.Notes != "urgent: line down" {
		t.Fatalf("create echo notes = %q", req.Notes)
	}
	newNotes := "line restored, still needed"
	req2, err := p.UpdateRequisition(ctx, req.ID, nil, nil, nil, nil, nil, nil, nil, nil, &newNotes, actor)
	if err != nil {
		t.Fatalf("update requisition: %v", err)
	}
	if req2.Notes != newNotes {
		t.Fatalf("update echo notes = %q", req2.Notes)
	}

	c, err := p.CreateContract(ctx, vendorID, "Supply agreement", nil, nil, 5000, "USD", "", 1200, actor)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	if c.CommittedVolume != 1200 {
		t.Fatalf("create echo committedVolume = %v", c.CommittedVolume)
	}
	vol := 1500.0
	c2, err := p.UpdateContract(ctx, c.ID, nil, nil, nil, nil, nil, nil, nil, &vol, actor)
	if err != nil {
		t.Fatalf("update contract: %v", err)
	}
	if c2.CommittedVolume != 1500 {
		t.Fatalf("update echo committedVolume = %v", c2.CommittedVolume)
	}

	// PO raised for the requisition returns the link, and PATCH can clear it.
	po, err := p.CreatePurchaseOrder(ctx, vendorID, "Linked PO", "USD", budgetID, req.ID, nil,
		[]models.PoLine{{ItemID: itemID, Qty: 1, Price: 50}}, actor)
	if err != nil {
		t.Fatalf("po: %v", err)
	}
	if po.RequisitionID != req.ID {
		t.Fatalf("create echo requisitionId = %q, want %q", po.RequisitionID, req.ID)
	}
	got, err := p.GetPurchaseOrder(ctx, po.ID)
	if err != nil {
		t.Fatalf("get po: %v", err)
	}
	if got.RequisitionID != req.ID {
		t.Fatalf("GetPurchaseOrder requisitionId = %q", got.RequisitionID)
	}
	empty := ""
	po2, err := p.UpdatePurchaseOrder(ctx, po.ID, nil, nil, nil, nil, nil, nil, nil, &empty, actor)
	if err != nil {
		t.Fatalf("clear po link: %v", err)
	}
	if po2.RequisitionID != "" {
		t.Fatalf("requisitionId after clear = %q", po2.RequisitionID)
	}

	rfq, err := p.CreateRfq(ctx, "Linked RFQ", nil, nil, req.ID, actor)
	if err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if rfq.RequisitionID != req.ID {
		t.Fatalf("rfq create echo requisitionId = %q", rfq.RequisitionID)
	}
	rfq2, err := p.UpdateRfq(ctx, rfq.ID, nil, nil, nil, nil, nil, &empty, actor)
	if err != nil {
		t.Fatalf("clear rfq link: %v", err)
	}
	if rfq2.RequisitionID != "" {
		t.Fatalf("rfq requisitionId after clear = %q", rfq2.RequisitionID)
	}
	rfq3, err := p.UpdateRfq(ctx, rfq.ID, nil, nil, nil, nil, nil, &req.ID, actor)
	if err != nil {
		t.Fatalf("relink rfq: %v", err)
	}
	if rfq3.RequisitionID != req.ID {
		t.Fatalf("rfq requisitionId after relink = %q", rfq3.RequisitionID)
	}

	// The read paths all see the columns.
	seed, err := NewSeed(p.pool).Load(ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	var seenReq, seenContract, seenRfq bool
	for _, r := range seed.Requisitions {
		if r.ID == req.ID {
			seenReq = r.Notes == newNotes
		}
	}
	for _, ct := range seed.Contracts {
		if ct.ID == c.ID {
			seenContract = ct.CommittedVolume == 1500
		}
	}
	for _, r := range seed.Rfqs {
		if r.ID == rfq.ID {
			seenRfq = r.RequisitionID == req.ID
		}
	}
	if !seenReq || !seenContract || !seenRfq {
		t.Fatalf("seed snapshot dropped a column: requisition=%v contract=%v rfq=%v", seenReq, seenContract, seenRfq)
	}
	reqs, err := p.ListRequisitions(ctx, 500, 0, "Notes test")
	if err != nil {
		t.Fatalf("list requisitions: %v", err)
	}
	seenReq = false
	for _, r := range reqs {
		if r.ID == req.ID && r.Notes == newNotes {
			seenReq = true
		}
	}
	if !seenReq {
		t.Fatal("paged requisition list dropped notes")
	}
	contracts, err := p.ListContracts(ctx, 500, 0, "")
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	seenContract = false
	for _, ct := range contracts {
		if ct.ID == c.ID && ct.CommittedVolume == 1500 {
			seenContract = true
		}
	}
	if !seenContract {
		t.Fatal("paged contract list dropped committedVolume")
	}
	rfqs, err := p.ListRfqs(ctx, 500, 0, "")
	if err != nil {
		t.Fatalf("list rfqs: %v", err)
	}
	seenRfq = false
	for _, r := range rfqs {
		if r.ID == rfq.ID && r.RequisitionID == req.ID {
			seenRfq = true
		}
	}
	if !seenRfq {
		t.Fatal("paged rfq list dropped requisitionId")
	}
}

// TestPresentAndEmptyClearsEveryDate pins the "present and empty clears"
// contract every PATCH handler documents for its date fields.
//
// It was never true. Each UPDATE used `CASE WHEN $n::date IS NULL THEN col
// ELSE $n::date END`, and "leave alone" and "clear" both arrive as SQL NULL,
// so a clear was a silent no-op on every date column in the service. The
// clear now travels as an explicit flag; this walks all seven columns.
func TestPresentAndEmptyClearsEveryDate(t *testing.T) {
	p, ctx := testProcurement(t)
	actor := "tester-" + uuid.NewString()
	vendorID, itemID, budgetID, _ := receivingFixture(t, p, ctx, 100)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var nilTime *time.Time
	clear := &nilTime
	keep := &day

	// requisitions.needed_by
	req, err := p.CreateRequisition(ctx, "Clear dates", "Ops", actor, "Medium", "", &day, 50, "USD", budgetID, "", actor)
	if err != nil {
		t.Fatalf("requisition: %v", err)
	}
	if req.NeededBy == "" {
		t.Fatalf("fixture: neededBy not stored")
	}
	if r, err := p.UpdateRequisition(ctx, req.ID, nil, nil, nil, nil, nil, nil, clear, nil, nil, actor); err != nil || r.NeededBy != "" {
		t.Fatalf("clear neededBy: err=%v neededBy=%q", err, r.NeededBy)
	}
	if r, err := p.UpdateRequisition(ctx, req.ID, nil, nil, nil, nil, nil, nil, &keep, nil, nil, actor); err != nil || r.NeededBy != "2026-10-01" {
		t.Fatalf("set neededBy: err=%v neededBy=%q", err, r.NeededBy)
	}

	// rfqs.due_date
	rfq, err := p.CreateRfq(ctx, "Clear dates", &day, []string{vendorID}, "", actor)
	if err != nil {
		t.Fatalf("rfq: %v", err)
	}
	if r, err := p.UpdateRfq(ctx, rfq.ID, nil, nil, clear, nil, nil, nil, actor); err != nil || r.DueDate != "" {
		t.Fatalf("clear rfq dueDate: err=%v dueDate=%q", err, r.DueDate)
	}

	// contracts.start_date / end_date
	c, err := p.CreateContract(ctx, vendorID, "Clear dates", &day, &day, 10, "USD", "", 0, actor)
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	if r, err := p.UpdateContract(ctx, c.ID, nil, nil, clear, nil, nil, nil, nil, nil, actor); err != nil || r.StartDate != "" || r.EndDate == "" {
		t.Fatalf("clear startDate only: err=%v start=%q end=%q", err, r.StartDate, r.EndDate)
	}
	if r, err := p.UpdateContract(ctx, c.ID, nil, nil, nil, clear, nil, nil, nil, nil, actor); err != nil || r.EndDate != "" {
		t.Fatalf("clear endDate: err=%v end=%q", err, r.EndDate)
	}

	// purchase_orders.expected_date
	po, err := p.CreatePurchaseOrder(ctx, vendorID, "Clear dates", "USD", budgetID, "", &day,
		[]models.PoLine{{ItemID: itemID, Qty: 1, Price: 10}}, actor)
	if err != nil {
		t.Fatalf("po: %v", err)
	}
	if r, err := p.UpdatePurchaseOrder(ctx, po.ID, nil, nil, nil, nil, clear, nil, nil, nil, actor); err != nil || r.ExpectedDate != "" {
		t.Fatalf("clear expectedDate: err=%v expected=%q", err, r.ExpectedDate)
	}

	// grns.received_date — clearing leaves NULL; the create default is today.
	grn, err := p.CreateGrn(ctx, vendorID, nil, actor, "Draft", &day, nil, false, "", "", "", actor)
	if err != nil {
		t.Fatalf("grn: %v", err)
	}
	if r, err := p.UpdateGrn(ctx, grn.ID, nil, nil, clear, nil, nil, nil, nil, nil, nil, nil, actor); err != nil || r.ReceivedDate != "" {
		t.Fatalf("clear receivedDate: err=%v received=%q", err, r.ReceivedDate)
	}

	// invoices.invoice_date and due_date
	inv, err := p.CreateInvoice(ctx, vendorID, nil, 10, "USD", &day, nil, "", &day, actor)
	if err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if r, err := p.UpdateInvoice(ctx, inv.ID, nil, nil, nil, nil, nil, nil, nil, clear, nil, nil, actor); err != nil || r.InvoiceDate != "" || r.DueDate == "" {
		t.Fatalf("clear invoiceDate only: err=%v invoiceDate=%q dueDate=%q", err, r.InvoiceDate, r.DueDate)
	}
	// And an update that never mentions a date leaves it alone.
	title := "untouched"
	if r, err := p.UpdateContract(ctx, c.ID, nil, &title, nil, nil, nil, nil, nil, nil, actor); err != nil || r.Title != title {
		t.Fatalf("unrelated patch: err=%v %+v", err, r)
	}
}

// TestListSearchWorksOnUUIDKeyedTables pins ?q= on every paged list.
//
// Migration 027 retyped id and vendor_id to uuid; the search clauses still
// said `id ILIKE $1`, which Postgres refuses ("operator does not exist: uuid
// ~~* unknown"). Every q= search on RFQs, orders, receipts, invoices,
// contracts and payments answered 500. The app's own lists never send q, so
// nothing noticed until the live run did.
func TestListSearchWorksOnUUIDKeyedTables(t *testing.T) {
	p, ctx := testProcurement(t)
	if _, err := p.ListRfqs(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("rfqs q=: %v", err)
	}
	if _, err := p.ListPurchaseOrders(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("purchase orders q=: %v", err)
	}
	if _, err := p.ListGrns(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("grns q=: %v", err)
	}
	if _, err := p.ListInvoices(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("invoices q=: %v", err)
	}
	if _, err := p.ListContracts(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("contracts q=: %v", err)
	}
	if _, err := p.ListPayments(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("payments q=: %v", err)
	}
	if _, err := p.ListVendors(ctx, 10, 0, "QA"); err != nil {
		t.Errorf("vendors q=: %v", err)
	}
}
