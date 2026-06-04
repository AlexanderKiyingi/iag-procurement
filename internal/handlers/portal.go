package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/middleware"
)

// PortalMe returns the vendor profile linked to the authenticated portal user.
func (a *API) PortalMe(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "portal user not linked"})
		return
	}
	var vendor struct {
		ID, Name, Category, Country, Contact, Email string
		PartyID                                     *string
	}
	err := a.pool.QueryRow(c.Request.Context(), `
		SELECT id, name, category, country, contact, email, party_id::text
		FROM vendors WHERE platform_user_id = $1::uuid`, uid).Scan(
		&vendor.ID, &vendor.Name, &vendor.Category, &vendor.Country,
		&vendor.Contact, &vendor.Email, &vendor.PartyID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor profile not linked"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": vendor})
}

// PortalPOs lists purchase orders for the linked vendor only.
func (a *API) PortalPOs(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "portal user not linked"})
		return
	}
	var vendorID string
	if err := a.pool.QueryRow(c.Request.Context(), `
		SELECT id FROM vendors WHERE platform_user_id = $1::uuid`, uid).Scan(&vendorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor profile not linked"})
		return
	}
	rows, err := a.pool.Query(c.Request.Context(), `
		SELECT id, title, total, currency, status, created_at::text
		FROM purchase_orders WHERE vendor_id = $1 ORDER BY created_at DESC LIMIT 50`, vendorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list POs"})
		return
	}
	defer rows.Close()
	type po struct {
		ID, Title, Currency, Status, CreatedAt string
		Total                                    float64
	}
	var list []po
	for rows.Next() {
		var p po
		if err := rows.Scan(&p.ID, &p.Title, &p.Total, &p.Currency, &p.Status, &p.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, p)
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// PortalInvoices lists invoices for the linked vendor only.
func (a *API) PortalInvoices(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "portal user not linked"})
		return
	}
	var vendorID string
	if err := a.pool.QueryRow(c.Request.Context(), `
		SELECT id FROM vendors WHERE platform_user_id = $1::uuid`, uid).Scan(&vendorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "vendor profile not linked"})
		return
	}
	rows, err := a.pool.Query(c.Request.Context(), `
		SELECT id, invoice_no, total, currency, status, due_date::text
		FROM invoices WHERE vendor_id = $1 ORDER BY due_date DESC LIMIT 50`, vendorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list invoices"})
		return
	}
	defer rows.Close()
	type inv struct {
		ID, InvoiceNo, Currency, Status, DueDate string
		Total                                      float64
	}
	var list []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.ID, &i.InvoiceNo, &i.Total, &i.Currency, &i.Status, &i.DueDate); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, i)
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}
