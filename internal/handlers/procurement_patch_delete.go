package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/models"
	"iag-procurement/backend/internal/signals"
)

// validRequisitionStatuses is the allowed set for a PATCH status transition.
var validRequisitionStatuses = map[string]bool{
	"draft":            true,
	"pending approval": true,
	"approved":         true,
	"rejected":         true,
	"ordered":          true,
	"closed":           true,
}

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

	// Reject unknown status values so a typo can't silently no-op the
	// downstream approval flow (only "approved"/"rejected" trigger events).
	if body.Status != nil && !validRequisitionStatuses[strings.ToLower(strings.TrimSpace(*body.Status))] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status; allowed: draft, pending approval, approved, rejected, ordered, closed"})
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

// emitRequisitionStatusChange raises an in-app signal when a PATCH transitions a
// requisition to a terminal status, so the requester is notified in-app. The
// DURABLE cross-service Kafka event (procurement.requisition.approved/.rejected)
// is now enqueued transactionally by the repo via the outbox — this is only the
// best-effort in-app companion and never fails the HTTP response.
func (a *API) emitRequisitionStatusChange(c *gin.Context, row *models.Requisition, status string) {
	if a.bus == nil || row == nil {
		return
	}
	outcome := strings.ToLower(strings.TrimSpace(status))
	if outcome != "approved" && outcome != "rejected" {
		return
	}
	body, _ := json.Marshal(map[string]string{"id": row.ID, "title": row.Title, "status": outcome})
	_ = a.bus.Emit(c.Request.Context(), signals.Event{Name: events.RequisitionDecided, Payload: body})
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
