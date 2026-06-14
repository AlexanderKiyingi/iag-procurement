package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/repo"
)

type closeBudgetPeriodBody struct {
	Policy   string `json:"policy" binding:"required"` // "lapse" | "carry"
	Period   string `json:"period"`                    // optional: limit to a period label
	BudgetID string `json:"budgetId"`                  // optional: a single budget
}

// closeBudgetPeriod lapses or carries forward open encumbrances for the matching
// budgets at period close.
func (a *API) closeBudgetPeriod(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body closeBudgetPeriodBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ids, err := a.procurement.CloseBudgetPeriod(c.Request.Context(), strings.TrimSpace(body.Policy),
		repo.BudgetCloseFilter{Period: strings.TrimSpace(body.Period), BudgetID: strings.TrimSpace(body.BudgetID)},
		authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"policy": strings.ToLower(strings.TrimSpace(body.Policy)), "closed": len(ids), "budgetIds": ids})
}
