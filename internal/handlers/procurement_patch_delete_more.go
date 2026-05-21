package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/models"
)

type patchItemBody struct {
	SKU               *string  `json:"sku"`
	Name              *string  `json:"name"`
	Category          *string  `json:"category"`
	Uom               *string  `json:"uom"`
	Stock             *float64 `json:"stock"`
	Reorder           *float64 `json:"reorder"`
	LastPrice         *float64 `json:"lastPrice"`
	Currency          *string  `json:"currency"`
	PreferredVendorID *string  `json:"preferredVendorId"` // if present and empty: clear
}

func (a *API) patchItem(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchItemBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var pref *string
	if body.PreferredVendorID != nil {
		s := strings.TrimSpace(*body.PreferredVendorID)
		pref = &s // empty string signals clear
	}
	row, err := a.procurement.UpdateItem(
		c.Request.Context(),
		id,
		body.SKU, body.Name, body.Category, body.Uom,
		body.Stock, body.Reorder, body.LastPrice, body.Currency,
		pref,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteItem(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteItem(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchBudgetBody struct {
	Code      *string  `json:"code"`
	Period    *string  `json:"period"`
	Allocated *float64 `json:"allocated"`
	Dept      *string  `json:"dept"`
}

func (a *API) patchBudget(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchBudgetBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.UpdateBudget(c.Request.Context(), id, body.Code, body.Period, body.Allocated, body.Dept, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteBudget(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteBudget(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchRfqBody struct {
	Title            *string   `json:"title"`
	Status           *string   `json:"status"`
	DueDate          *string   `json:"dueDate"`          // if present and empty: clear
	WinnerVendorID   *string   `json:"winnerVendorId"`   // if present and empty: clear
	InvitedVendorIDs *[]string `json:"invitedVendorIds"` // if present: replace entire list
}

func (a *API) patchRfq(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchRfqBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var duePtr **time.Time
	if body.DueDate != nil {
		s := strings.TrimSpace(*body.DueDate)
		if s == "" {
			var nilTime *time.Time
			duePtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "dueDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			duePtr = &tmp
		}
	}
	var winner *string
	if body.WinnerVendorID != nil {
		s := strings.TrimSpace(*body.WinnerVendorID)
		winner = &s // empty clears
	}
	row, err := a.procurement.UpdateRfq(
		c.Request.Context(),
		id,
		body.Title,
		body.Status,
		duePtr,
		winner,
		body.InvitedVendorIDs,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteRfq(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteRfq(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchContractBody struct {
	VendorID  *string  `json:"vendorId"`
	Title     *string  `json:"title"`
	StartDate *string  `json:"startDate"` // if present and empty: clear
	EndDate   *string  `json:"endDate"`   // if present and empty: clear
	Value     *float64 `json:"value"`
	Currency  *string  `json:"currency"`
	Status    *string  `json:"status"`
}

func (a *API) patchContract(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchContractBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var startPtr **time.Time
	if body.StartDate != nil {
		s := strings.TrimSpace(*body.StartDate)
		if s == "" {
			var nilTime *time.Time
			startPtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "startDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			startPtr = &tmp
		}
	}
	var endPtr **time.Time
	if body.EndDate != nil {
		s := strings.TrimSpace(*body.EndDate)
		if s == "" {
			var nilTime *time.Time
			endPtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "endDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			endPtr = &tmp
		}
	}
	row, err := a.procurement.UpdateContract(
		c.Request.Context(),
		id,
		body.VendorID,
		body.Title,
		startPtr,
		endPtr,
		body.Value,
		body.Currency,
		body.Status,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteContract(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteContract(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchInvoiceBody struct {
	InvoiceNo  *string  `json:"invoiceNo"`  // if present and empty: clear
	VendorID   *string  `json:"vendorId"`
	PoID       *string  `json:"poId"`       // if present and empty: clear
	Amount     *float64 `json:"amount"`
	Currency   *string  `json:"currency"`
	Status     *string  `json:"status"`
	MatchStatus *string `json:"matchStatus"`
	InvoiceDate *string `json:"invoiceDate"` // if present and empty: clear
}

func (a *API) patchInvoice(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchInvoiceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var po *string
	if body.PoID != nil {
		s := strings.TrimSpace(*body.PoID)
		po = &s // empty clears
	}
	var invNo *string
	if body.InvoiceNo != nil {
		s := strings.TrimSpace(*body.InvoiceNo)
		invNo = &s // empty clears
	}
	var datePtr **time.Time
	if body.InvoiceDate != nil {
		s := strings.TrimSpace(*body.InvoiceDate)
		if s == "" {
			var nilTime *time.Time
			datePtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invoiceDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			datePtr = &tmp
		}
	}
	row, err := a.procurement.UpdateInvoice(
		c.Request.Context(),
		id,
		invNo,
		body.VendorID,
		po,
		body.Amount,
		body.Currency,
		body.Status,
		body.MatchStatus,
		datePtr,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteInvoice(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteInvoice(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchPoBody struct {
	VendorID     *string        `json:"vendorId"`
	Title        *string        `json:"title"`
	Currency     *string        `json:"currency"`
	Status       *string        `json:"status"`
	ExpectedDate *string        `json:"expectedDate"` // if present and empty: clear
	BudgetID     *string        `json:"budgetId"`
	Items        *[]models.PoLine `json:"items"`      // if present: replace all lines and recompute total
}

func (a *API) patchPurchaseOrder(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchPoBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var exPtr **time.Time
	if body.ExpectedDate != nil {
		s := strings.TrimSpace(*body.ExpectedDate)
		if s == "" {
			var nilTime *time.Time
			exPtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "expectedDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			exPtr = &tmp
		}
	}
	row, err := a.procurement.UpdatePurchaseOrder(
		c.Request.Context(),
		id,
		body.VendorID,
		body.Title,
		body.Currency,
		body.Status,
		exPtr,
		body.BudgetID,
		body.Items,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deletePurchaseOrder(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeletePurchaseOrder(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

type patchGrnBody struct {
	PoID         *string `json:"poId"` // if present and empty: clear
	VendorID     *string `json:"vendorId"`
	ReceivedDate *string `json:"receivedDate"` // if present and empty: clear
	ReceivedBy   *string `json:"receivedBy"`
	Status       *string `json:"status"`
}

func (a *API) patchGrn(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var body patchGrnBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var po *string
	if body.PoID != nil {
		s := strings.TrimSpace(*body.PoID)
		po = &s
	}
	var datePtr **time.Time
	if body.ReceivedDate != nil {
		s := strings.TrimSpace(*body.ReceivedDate)
		if s == "" {
			var nilTime *time.Time
			datePtr = &nilTime
		} else {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "receivedDate must be YYYY-MM-DD"})
				return
			}
			utc := t.UTC()
			tmp := &utc
			datePtr = &tmp
		}
	}
	row, err := a.procurement.UpdateGrn(
		c.Request.Context(),
		id,
		body.VendorID,
		po,
		datePtr,
		body.ReceivedBy,
		body.Status,
		authActorEmail(c),
	)
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusOK, row)
}

func (a *API) deleteGrn(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	err := a.procurement.DeleteGrn(c.Request.Context(), id, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.Status(http.StatusNoContent)
}

