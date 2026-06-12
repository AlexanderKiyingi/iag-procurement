package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type postVendorBody struct {
	Name     string  `json:"name" binding:"required"`
	Logo     string  `json:"logo"`
	Category string  `json:"category"`
	Contact  string  `json:"contact"`
	Email    string  `json:"email"`
	Phone    string  `json:"phone"`
	Country  string  `json:"country"`
	Terms    string  `json:"terms"`
	Rating   float64 `json:"rating"`
	Status   string  `json:"status"`
}

func (a *API) postVendor(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postVendorBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.CreateVendor(c.Request.Context(),
		strings.TrimSpace(body.Name), strings.TrimSpace(body.Logo), strings.TrimSpace(body.Category),
		strings.TrimSpace(body.Contact), strings.TrimSpace(body.Email), strings.TrimSpace(body.Phone),
		strings.TrimSpace(body.Country), strings.TrimSpace(body.Terms), body.Rating, strings.TrimSpace(body.Status),
		authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postItemBody struct {
	SKU               string  `json:"sku" binding:"required"`
	Name              string  `json:"name" binding:"required"`
	Category          string  `json:"category"`
	Uom               string  `json:"uom"`
	Stock             float64 `json:"stock"`
	Reorder           float64 `json:"reorder"`
	LastPrice         float64 `json:"lastPrice"`
	Currency          string  `json:"currency"`
	PreferredVendorID string  `json:"preferredVendorId"`
}

func (a *API) postItem(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postItemBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.CreateItem(c.Request.Context(),
		body.SKU, body.Name, strings.TrimSpace(body.Category), strings.TrimSpace(body.Uom),
		body.Stock, body.Reorder, body.LastPrice, strings.TrimSpace(body.Currency),
		body.PreferredVendorID,
		authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postBudgetBody struct {
	Code      string  `json:"code" binding:"required"`
	Period    string  `json:"period"`
	Allocated float64 `json:"allocated"`
	Dept      string  `json:"dept"`
}

func (a *API) postBudget(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postBudgetBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := a.procurement.CreateBudget(c.Request.Context(),
		strings.TrimSpace(body.Code), strings.TrimSpace(body.Period), body.Allocated, strings.TrimSpace(body.Dept),
		authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postRfqBody struct {
	Title            string   `json:"title" binding:"required"`
	DueDate          string   `json:"dueDate"`
	InvitedVendorIDs []string `json:"invitedVendorIds"`
	RequisitionID    string   `json:"requisitionId"` // optional: source requisition for traceability
}

func (a *API) postRfq(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postRfqBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	due, err := parseOptionalDay("2006-01-02", body.DueDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dueDate must be YYYY-MM-DD"})
		return
	}
	row, err := a.procurement.CreateRfq(c.Request.Context(),
		strings.TrimSpace(body.Title), due, body.InvitedVendorIDs, strings.TrimSpace(body.RequisitionID), authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postGrnBody struct {
	VendorID     string  `json:"vendorId" binding:"required"`
	PoID         *string `json:"poId"`
	ReceivedBy   string  `json:"receivedBy"`
	Status       string  `json:"status"`
	ReceivedDate string  `json:"receivedDate"`
}

func (a *API) postGrn(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postGrnBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rd, err := parseOptionalDay("2006-01-02", body.ReceivedDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "receivedDate must be YYYY-MM-DD"})
		return
	}
	var poID *string
	if body.PoID != nil {
		s := strings.TrimSpace(*body.PoID)
		if s != "" {
			poID = &s
		}
	}
	row, err := a.procurement.CreateGrn(c.Request.Context(),
		strings.TrimSpace(body.VendorID), poID, strings.TrimSpace(body.ReceivedBy), strings.TrimSpace(body.Status),
		rd, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.emitGrnPostedIfNeeded(c.Request.Context(), row)
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postInvoiceBody struct {
	VendorID    string  `json:"vendorId" binding:"required"`
	PoID        *string `json:"poId"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	InvoiceDate string  `json:"invoiceDate"`
	InvoiceNo   *string `json:"invoiceNo"`
}

func (a *API) postInvoice(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postInvoiceBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	idate, err := parseOptionalDay("2006-01-02", body.InvoiceDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invoiceDate must be YYYY-MM-DD"})
		return
	}
	var poID *string
	if body.PoID != nil {
		s := strings.TrimSpace(*body.PoID)
		if s != "" {
			poID = &s
		}
	}
	row, err := a.procurement.CreateInvoice(c.Request.Context(),
		strings.TrimSpace(body.VendorID), poID, body.Amount, strings.TrimSpace(body.Currency),
		idate, body.InvoiceNo, authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	if a.publisher != nil && row != nil {
		docRef := row.ID
		if row.InvoiceNo != nil && strings.TrimSpace(*row.InvoiceNo) != "" {
			docRef = strings.TrimSpace(*row.InvoiceNo)
		}
		currency := row.Currency
		if currency == "" {
			currency = "UGX"
		}
		var due *time.Time
		if row.InvoiceDate != "" {
			if t, err := time.Parse("2006-01-02", row.InvoiceDate); err == nil {
				due = &t
			}
		}
		a.publisher.PublishInvoiceReceived(c.Request.Context(), docRef, row.VendorID,
			fmt.Sprintf("%.2f", row.Amount), currency, due)
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}

type postContractBody struct {
	VendorID  string  `json:"vendorId" binding:"required"`
	Title     string  `json:"title" binding:"required"`
	StartDate string  `json:"startDate"`
	EndDate   string  `json:"endDate"`
	Value     float64 `json:"value"`
	Currency  string  `json:"currency"`
	Status    string  `json:"status"`
}

func (a *API) postContract(c *gin.Context) {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return
	}
	var body postContractBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sd, err := parseOptionalDay("2006-01-02", body.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startDate must be YYYY-MM-DD"})
		return
	}
	ed, err := parseOptionalDay("2006-01-02", body.EndDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endDate must be YYYY-MM-DD"})
		return
	}
	row, err := a.procurement.CreateContract(c.Request.Context(),
		strings.TrimSpace(body.VendorID), strings.TrimSpace(body.Title), sd, ed, body.Value,
		strings.TrimSpace(body.Currency), strings.TrimSpace(body.Status), authActorEmail(c))
	if mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())
	c.JSON(http.StatusCreated, row)
}
