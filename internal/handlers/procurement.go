package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/models"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/repo"
)

type postRequisitionBody struct {
	Title    string  `json:"title" binding:"required"`
	Dept     string  `json:"dept"`
	Priority string  `json:"priority"`
	NeededBy string  `json:"neededBy"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
	BudgetID string  `json:"budgetId" binding:"required"`
}

type postPurchaseOrderBody struct {
	VendorID      string          `json:"vendorId" binding:"required"`
	Title         string          `json:"title" binding:"required"`
	BudgetID      string          `json:"budgetId" binding:"required"`
	Currency      string          `json:"currency"`
	ExpectedDate  string          `json:"expectedDate"`
	RequisitionID string          `json:"requisitionId"` // optional: source requisition for traceability
	Items         []models.PoLine `json:"items" binding:"required,min=1"`
}

func authActorEmail(c *gin.Context) string {
	v, _ := c.Get(middleware.CtxEmail)
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func parseOptionalDay(layout, s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return nil, err
	}
	utc := t.UTC()
	return &utc, nil
}

func mapProcurementErr(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repo.ErrInvalidArgument) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return true
	}
	if errors.Is(err, repo.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return true
	}
	if errors.Is(err, repo.ErrForbidden) {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return true
	}
	if errors.Is(err, repo.ErrConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return true
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid reference (budget, vendor, or item)", "detail": pe.Message})
		return true
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	return true
}

// getPurchaseOrder returns a single PO with its lines (including received_qty)
// so the receiving UI can pre-fill a GRN against it.
func (a *API) getPurchaseOrder(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "purchase-order lookup requires the database-backed store"})
		return
	}
	row, err := a.procurement.GetPurchaseOrder(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, row)
}

// approveInvoice clears a matched invoice for payment. Returns 409 when the
// three-way match has not resolved to "Matched".
func (a *API) approveInvoice(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	row, err := a.procurement.ApproveInvoice(c.Request.Context(), strings.TrimSpace(c.Param("id")), authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	// Clearing an invoice for payment is the gate that releases money, and it
	// notified nobody: the AP desk had to poll the list to discover anything
	// had been approved. Addressed to the audience so an administrator decides
	// who sits on that desk without a redeploy.
	// Approval moves the invoice's status and writes an audit entry, both of
	// which sit in the cached seed payload. Every other write handler drops
	// that cache; this one did not, so an approved invoice still read as
	// unapproved to any caller that had not opted into paging.
	a.InvalidateSeedCache(c.Request.Context())
	a.notifyInvoiceApprovedAsync(c, row)
	c.JSON(http.StatusOK, row)
}

// notifyInvoiceApprovedAsync tells the payables desk an invoice cleared.
//
// Detached from the request context for the same reason the desk-chain
// notifier is (see notifyDeskTransitionAsync): gin cancels the request context
// as soon as the response is written, which would cancel the dispatch the
// instant it mattered. Best-effort and after the commit — the approval is
// already durable, and a lost email must never undo it.
func (a *API) notifyInvoiceApprovedAsync(c *gin.Context, inv *models.Invoice) {
	if a.notify == nil || inv == nil {
		return
	}
	amount := fmt.Sprintf("%s %.2f", inv.Currency, inv.Amount)
	ref := inv.ID
	if inv.InvoiceNo != nil && strings.TrimSpace(*inv.InvoiceNo) != "" {
		ref = strings.TrimSpace(*inv.InvoiceNo)
	}
	detail := "Vendor " + inv.VendorID + ". Three-way match: " + inv.MatchStatus + "."
	if inv.PoID != nil && strings.TrimSpace(*inv.PoID) != "" {
		detail += " PO " + strings.TrimSpace(*inv.PoID) + "."
	}
	payload := notifications.AlertJobPayload{
		Audience: invoiceApprovalAudience,
		To:       nonEmpty([]string{defaultNotifyRecipient()}),
		Title:    "Invoice approved for payment: " + ref,
		Message:  "Invoice " + ref + " (" + amount + ") was approved for payment by " + authActorEmail(c) + ".",
		Detail:   detail,
	}
	ctx := context.WithoutCancel(c.Request.Context())
	go func() {
		ctx, cancel := context.WithTimeout(ctx, deskNotifyTimeout)
		defer cancel()
		if err := a.notify.EnqueueAlertEmail(ctx, payload); err != nil {
			log.Printf("invoice approval notification for %s: %v", ref, err)
		}
	}()
}

// invoiceApprovalAudience is the desk told when an invoice clears for payment.
const invoiceApprovalAudience = "approvals.procurement"

// defaultNotifyRecipient is the fallback address used until the audience above
// has been routed. Matches the NOTIFY_DEFAULT_RECIPIENT convention every other
// alert emitter on the platform follows.
func defaultNotifyRecipient() string {
	return strings.TrimSpace(os.Getenv("NOTIFY_DEFAULT_RECIPIENT"))
}

func (a *API) postRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postRequisitionBody
	if err := bindJSONCoerced(c, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	neededBy, err := parseOptionalDay("2006-01-02", body.NeededBy)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "neededBy must be YYYY-MM-DD"})
		return
	}
	row, err := a.procurement.CreateRequisition(
		c.Request.Context(),
		strings.TrimSpace(body.Title),
		strings.TrimSpace(body.Dept),
		authActorEmail(c),
		body.Priority,
		"",
		neededBy,
		body.Total,
		body.Currency,
		strings.TrimSpace(body.BudgetID),
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

func (a *API) postPurchaseOrder(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postPurchaseOrderBody
	if err := bindJSONCoerced(c, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ex, err := parseOptionalDay("2006-01-02", body.ExpectedDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expectedDate must be YYYY-MM-DD"})
		return
	}
	row, err := a.procurement.CreatePurchaseOrder(
		c.Request.Context(),
		strings.TrimSpace(body.VendorID),
		strings.TrimSpace(body.Title),
		body.Currency,
		strings.TrimSpace(body.BudgetID),
		strings.TrimSpace(body.RequisitionID),
		ex,
		body.Items,
		authActorEmail(c),
	)
	if err != nil && strings.Contains(err.Error(), "at least one line") {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil && strings.Contains(err.Error(), "invalid line") {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}
