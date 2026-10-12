package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// patchVendorQualityStatus records procurement's quality decision on a
// supplier: approved, conditional, suspended (or "" to clear), with a reason.
func (a *API) patchVendorQualityStatus(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if mapProcurementErr(c, a.procurement.SetVendorQualityStatus(c.Request.Context(), id, body.Status, body.Reason, authActorEmail(c))) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"id": id, "qualityStatus": strings.ToLower(strings.TrimSpace(body.Status))})
}
