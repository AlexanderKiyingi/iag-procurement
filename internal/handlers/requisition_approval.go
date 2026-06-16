package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/models"
)

// bindOptionalNote binds the approve/reject body, tolerating an empty request
// body (the note is optional). It reports false and writes a 400 on malformed JSON.
func bindOptionalNote(c *gin.Context, body *approvalBody) bool {
	if err := c.ShouldBindJSON(body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	return true
}

type approvalBody struct {
	Note string `json:"note"`
}

// listApprovalTiers returns the editable amount-band approval matrix so the UI
// can render which permission gates each tier.
func (a *API) listApprovalTiers(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	tiers, err := a.procurement.ListApprovalTiers(c.Request.Context())
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, tiers)
}

// approveRequisition records the caller's tier signature and finalises the
// requisition once every required band has signed. The route is gated by
// change_requisition; the per-tier permission is enforced inside the repo
// against the configured matrix.
func (a *API) approveRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body approvalBody
	if !bindOptionalNote(c, &body) {
		return
	}
	hasPerm := func(code string) bool { return middleware.HasPerm(c, code) }
	row, progress, err := a.procurement.ApproveRequisitionTier(
		c.Request.Context(), id, authActorEmail(c), hasPerm, strings.TrimSpace(body.Note))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	if progress != nil && progress.Complete {
		a.emitRequisitionStatusChange(c, row, "approved")
	}
	c.JSON(http.StatusOK, approvalResponse(row, progress))
}

// rejectRequisition rejects a requisition in the tiered flow and releases any
// budget pre-encumbrance.
func (a *API) rejectRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body approvalBody
	if !bindOptionalNote(c, &body) {
		return
	}
	hasPerm := func(code string) bool { return middleware.HasPerm(c, code) }
	row, progress, err := a.procurement.RejectRequisitionTiered(
		c.Request.Context(), id, authActorEmail(c), hasPerm, strings.TrimSpace(body.Note))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	a.emitRequisitionStatusChange(c, row, "rejected")
	c.JSON(http.StatusOK, approvalResponse(row, progress))
}

func approvalResponse(row *models.Requisition, progress interface{}) gin.H {
	return gin.H{"requisition": row, "approval": progress}
}
