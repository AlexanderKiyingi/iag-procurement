package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/models"
	"iag-procurement/backend/internal/repo"
)

type patchVendorBody struct {
	Name     *string  `json:"name"`
	Logo     *string  `json:"logo"`
	Category *string  `json:"category"`
	Contact  *string  `json:"contact"`
	Email    *string  `json:"email"`
	Phone    *string  `json:"phone"`
	Country  *string  `json:"country"`
	Terms    *string  `json:"terms"`
	Rating   *float64 `json:"rating"`
	Status   *string  `json:"status"`
}

func (a *API) patchVendor(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchVendorBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.UpdateVendor(
		c.Request.Context(),
		id,
		body.Name, body.Logo, body.Category, body.Contact, body.Email, body.Phone, body.Country, body.Terms,
		body.Rating,
		body.Status,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteVendor(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteVendor(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchRequisitionBody struct {
	Title    *string  `json:"title"`
	Dept     *string  `json:"dept"`
	Priority *string  `json:"priority"`
	Status   *string  `json:"status"`
	NeededBy *string  `json:"neededBy"` // if present and empty: clear
	Total    *float64 `json:"total"`
	Currency *string  `json:"currency"`
	BudgetID *string  `json:"budgetId"`
}

func (a *API) patchRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchRequisitionBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var neededPtr **time.Time
	if body.NeededBy != nil {
		s := strings.TrimSpace(*body.NeededBy)
		if s == "" {
			var nilTime *time.Time
			neededPtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "neededBy must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			neededPtr = &tmp
		}
	}

	row, err := a.procurement.UpdateRequisition(
		c.Request.Context(),
		id,
		body.Title,
		body.Dept,
		body.Priority,
		body.Status,
		body.Currency,
		body.BudgetID,
		neededPtr,
		body.Total,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	if body.Status != nil {
		a.emitRequisitionStatusChange(c, row, *body.Status)
	}
	c.JSON(http.StatusOK, row)
}

// emitRequisitionStatusChange publishes procurement.requisition.approved or
// .rejected on iag.commercial when a PATCH transitions to a terminal status.
// PM-imported requisitions carry workspaceOwnerUserId so the originating PM
// workspace can be located by the consumer. Best-effort — never fails the HTTP
// response.
func (a *API) emitRequisitionStatusChange(c *gin.Context, row *models.Requisition, status string) {
	if a.publisher == nil || !a.publisher.Enabled() || row == nil {
		return
	}
	outcome := strings.ToLower(strings.TrimSpace(status))
	if outcome != "approved" && outcome != "rejected" {
		return
	}
	link, err := a.procurement.GetPMLink(c.Request.Context(), row.ID)
	if err != nil {
		// Failed lookup shouldn't block the publish — still emit with the
		// procurement id, just without PM routing.
		link = repo.PMLink{}
	}
	actor := authActorEmail(c)
	switch outcome {
	case "approved":
		a.publisher.PublishRequisitionApproved(c.Request.Context(), row.ID, link.PMRequisitionID, link.PMWorkspaceOwner, actor, row.BudgetID)
	case "rejected":
		a.publisher.PublishRequisitionRejected(c.Request.Context(), row.ID, link.PMRequisitionID, link.PMWorkspaceOwner, actor, row.BudgetID)
	}
}

func (a *API) deleteRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteRequisition(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

