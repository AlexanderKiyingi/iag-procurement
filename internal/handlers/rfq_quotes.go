package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// listRfqQuotes returns the buyer-recorded quotes for an RFQ.
func (a *API) listRfqQuotes(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	rows, err := a.procurement.ListRfqQuotes(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, rows)
}

type postRfqQuoteBody struct {
	VendorID string  `json:"vendorId" binding:"required"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Notes    string  `json:"notes"`
}

// postRfqQuote records a vendor quote against an open RFQ.
func (a *API) postRfqQuote(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postRfqQuoteBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.CreateRfqQuote(
		c.Request.Context(),
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(body.VendorID),
		body.Amount,
		strings.TrimSpace(body.Currency),
		body.Notes,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusCreated, row)
}

type awardRfqBody struct {
	QuoteID      string `json:"quoteId"`
	VendorID     string `json:"vendorId"`
	BudgetID     string `json:"budgetId"`
	ExpectedDate string `json:"expectedDate"`
}

// awardRfq awards a winning quote and creates the resulting draft PO.
func (a *API) awardRfq(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body awardRfqBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ex, err := parseOptionalDay("2006-01-02", body.ExpectedDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expectedDate must be YYYY-MM-DD"})
		return
	}
	po, err := a.procurement.AwardRfq(
		c.Request.Context(),
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(body.QuoteID),
		strings.TrimSpace(body.VendorID),
		strings.TrimSpace(body.BudgetID),
		ex,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, po)
}
