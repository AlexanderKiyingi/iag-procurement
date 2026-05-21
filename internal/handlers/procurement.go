package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/models"
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
	VendorID     string          `json:"vendorId" binding:"required"`
	Title        string          `json:"title" binding:"required"`
	BudgetID     string          `json:"budgetId" binding:"required"`
	Currency     string          `json:"currency"`
	ExpectedDate string          `json:"expectedDate"`
	Items        []models.PoLine `json:"items" binding:"required,min=1"`
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
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid reference (budget, vendor, or item)", "detail": pe.Message})
		return true
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	return true
}

func (a *API) postRequisition(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postRequisitionBody
	if err := c.ShouldBindJSON(&body); err != nil {
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
	if err := c.ShouldBindJSON(&body); err != nil {
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
