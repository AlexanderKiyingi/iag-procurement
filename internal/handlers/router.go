package handlers

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"iag-procurement/backend/internal/auditlog"
	"iag-procurement/backend/internal/cache"
	"iag-procurement/backend/internal/config"
	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/iam"
	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/models"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/rbac"
	"iag-procurement/backend/internal/repo"
	"iag-procurement/backend/internal/signals"
)

type API struct {
	pool         *pgxpool.Pool
	seed         *repo.Seed
	procurement  *repo.Procurement
	cache        *cache.Client
	cfg          *config.Config
	notify       *notifications.Service
	bus          *signals.Bus
	iam          *iam.Service
	rbac         *rbac.Store
	audit        *auditlog.Store
	platformAuth *middleware.PlatformAuth
	publisher    *events.Publisher
}

func NewAPI(d Deps) *API {
	return &API{
		pool:         d.Pool,
		seed:         d.Seed,
		procurement:  d.Procurement,
		cache:        d.Cache,
		cfg:          d.Config,
		notify:       d.Notify,
		bus:          d.Bus,
		iam:          d.IAM,
		rbac:         d.RBAC,
		audit:        d.Audit,
		platformAuth: d.PlatformAuth,
		publisher:    d.Publisher,
	}
}

func (a *API) Mount(r *gin.Engine) {
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.CORS(a.cfg.CORSAllowOrigin))
	r.GET("/health", a.health)
	r.GET("/healthz", a.health)
	r.GET("/ready", a.ready)

	if a.rbac == nil {
		log.Fatal("api: Deps.RBAC is required (permission checks)")
	}

	legacyAuth := a.cfg.AuthMode == "legacy"
	if legacyAuth && a.iam == nil {
		log.Fatal("api: Deps.IAM is required when AUTH_MODE=legacy")
	}
	if !legacyAuth && a.platformAuth == nil {
		log.Fatal("api: Deps.PlatformAuth is required when AUTH_MODE=gateway or jwt")
	}

	if a.platformAuth != nil {
		r.Use(a.platformAuth.AttachPrincipal())
	}

	v1 := r.Group("/api/v1")
	if a.audit != nil {
		v1.Use(middleware.RequestAudit(a.audit))
	}
	{
		if legacyAuth {
			v1.POST("/auth/login", a.postLogin)
		}

		sec := v1.Group("")
		if legacyAuth {
			sec.Use(middleware.JWTAuth(a.iam))
		} else {
			sec.Use(a.platformAuth.RequireAuth())
		}
		{
			sec.GET("/auth/me", a.getMe)

			data := sec.Group("")
			data.Use(middleware.RequirePermission(rbac.ViewSeed))
			{
				data.GET("/seed", a.getSeed)
				data.GET("/vendors", a.listVendors)
				data.GET("/items", a.listItems)
				data.GET("/budgets", a.listBudgets)
				data.GET("/requisitions", a.listRequisitions)
				data.GET("/rfqs", a.listRfqs)
				data.GET("/rfqs/:id/quotes", a.listRfqQuotes)
				data.GET("/purchase-orders", a.listPOs)
				data.GET("/orders", a.listPOs)
				data.GET("/grns", a.listGrns)
				data.GET("/invoices", a.listInvoices)
				data.GET("/contracts", a.listContracts)
				data.GET("/payments", a.listPayments)
				data.GET("/audit", a.listAudit)
			}

			mut := sec.Group("")
			mut.POST("/requisitions", middleware.RequirePermission(rbac.AddRequisition), a.postRequisition)
			mut.PATCH("/requisitions/:id", middleware.RequirePermission(rbac.ChangeRequisition), a.patchRequisition)
			mut.DELETE("/requisitions/:id", middleware.RequirePermission(rbac.DeleteRequisition), a.deleteRequisition)
			mut.POST("/purchase-orders", middleware.RequirePermission(rbac.AddPurchaseOrder), a.postPurchaseOrder)
			mut.PATCH("/purchase-orders/:id", middleware.RequirePermission(rbac.ChangePurchaseOrder), a.patchPurchaseOrder)
			mut.DELETE("/purchase-orders/:id", middleware.RequirePermission(rbac.DeletePurchaseOrder), a.deletePurchaseOrder)
			mut.POST("/vendors", middleware.RequirePermission(rbac.AddVendor), a.postVendor)
			mut.PATCH("/vendors/:id", middleware.RequirePermission(rbac.ChangeVendor), a.patchVendor)
			mut.DELETE("/vendors/:id", middleware.RequirePermission(rbac.DeleteVendor), a.deleteVendor)
			mut.POST("/items", middleware.RequirePermission(rbac.AddItem), a.postItem)
			mut.PATCH("/items/:id", middleware.RequirePermission(rbac.ChangeItem), a.patchItem)
			mut.DELETE("/items/:id", middleware.RequirePermission(rbac.DeleteItem), a.deleteItem)
			mut.POST("/budgets", middleware.RequirePermission(rbac.AddBudget), a.postBudget)
			mut.PATCH("/budgets/:id", middleware.RequirePermission(rbac.ChangeBudget), a.patchBudget)
			mut.DELETE("/budgets/:id", middleware.RequirePermission(rbac.DeleteBudget), a.deleteBudget)
			mut.POST("/rfqs", middleware.RequirePermission(rbac.AddRfq), a.postRfq)
			mut.PATCH("/rfqs/:id", middleware.RequirePermission(rbac.ChangeRfq), a.patchRfq)
			mut.DELETE("/rfqs/:id", middleware.RequirePermission(rbac.DeleteRfq), a.deleteRfq)
			mut.POST("/rfqs/:id/quotes", middleware.RequirePermission(rbac.ChangeRfq), a.postRfqQuote)
			mut.POST("/rfqs/:id/award", middleware.RequirePermission(rbac.ChangeRfq), a.awardRfq)
			mut.POST("/grns", middleware.RequirePermission(rbac.AddGrn), a.postGrn)
			mut.PATCH("/grns/:id", middleware.RequirePermission(rbac.ChangeGrn), a.patchGrn)
			mut.DELETE("/grns/:id", middleware.RequirePermission(rbac.DeleteGrn), a.deleteGrn)
			mut.POST("/invoices", middleware.RequirePermission(rbac.AddInvoice), a.postInvoice)
			mut.PATCH("/invoices/:id", middleware.RequirePermission(rbac.ChangeInvoice), a.patchInvoice)
			mut.DELETE("/invoices/:id", middleware.RequirePermission(rbac.DeleteInvoice), a.deleteInvoice)
			mut.POST("/contracts", middleware.RequirePermission(rbac.AddContract), a.postContract)
			mut.PATCH("/contracts/:id", middleware.RequirePermission(rbac.ChangeContract), a.patchContract)
			mut.DELETE("/contracts/:id", middleware.RequirePermission(rbac.DeleteContract), a.deleteContract)

			inbox := sec.Group("")
			inbox.Use(middleware.RequirePermission(rbac.ViewInbox))
			{
				inbox.GET("/notifications", a.listNotifications)
				inbox.PATCH("/notifications/:id/read", a.markNotificationRead)
			}

			emit := sec.Group("")
			emit.Use(middleware.RequirePermission(rbac.EmitNotification))
			emit.POST("/notifications/emit", a.emitNotification)

			portal := sec.Group("/portal")
			portal.GET("/me", middleware.RequirePermission(rbac.ViewOwnPO), a.PortalMe)
			portal.GET("/purchase-orders", middleware.RequirePermission(rbac.ViewOwnPO), a.PortalPOs)
			portal.GET("/invoices", middleware.RequirePermission(rbac.ViewOwnInvoice), a.PortalInvoices)

			al := sec.Group("")
			al.Use(middleware.RequirePermission(rbac.ViewAPIAudit))
			al.GET("/admin/audit-logs", a.listAPIAuditLogs)

			if legacyAuth {
				ap := sec.Group("")
				ap.Use(middleware.RequirePermission(rbac.ViewPermission))
				ap.GET("/admin/permissions", a.listAdminPermissions)

				ag := sec.Group("")
				ag.Use(middleware.RequirePermission(rbac.ViewGroup))
				ag.GET("/admin/groups", a.listAdminGroups)

				au := sec.Group("")
				au.Use(middleware.RequirePermission(rbac.ViewUser))
				au.GET("/admin/users", a.listAdminUsers)
				au.POST("/admin/users", middleware.RequirePermission(rbac.ChangeUser), a.postAdminCreateUser)
				au.PATCH("/admin/users/:id/password", middleware.RequirePermission(rbac.ChangeUser), a.patchAdminUserPassword)
				au.PATCH("/admin/users/:id", middleware.RequirePermission(rbac.ChangeUser), a.patchAdminUser)
				au.PUT("/admin/users/:id/groups", middleware.RequirePermission(rbac.ChangeUser), a.putAdminUserGroups)

				agMut := sec.Group("")
				agMut.Use(middleware.RequirePermission(rbac.ViewGroup))
				agMut.Use(middleware.RequirePermission(rbac.ChangeGroup))
				agMut.PUT("/admin/groups/:id/permissions", a.putGroupPermissions)
			} else {
				adminGone := sec.Group("/admin")
				adminGone.GET("/*path", a.adminIAMDeprecated)
				adminGone.POST("/*path", a.adminIAMDeprecated)
				adminGone.PATCH("/*path", a.adminIAMDeprecated)
				adminGone.PUT("/*path", a.adminIAMDeprecated)
				adminGone.DELETE("/*path", a.adminIAMDeprecated)
			}
		}
	}
}

func (a *API) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": a.cfg.ServiceName})
}

func (a *API) adminIAMDeprecated(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"error": gin.H{
			"code":    "GONE",
			"message": "Embedded admin IAM removed; use iag-authentication admin API (/api/v1/authentication/v1/admin) for users, groups, and permissions.",
		},
	})
}

func (a *API) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if a.pool != nil {
		if err := a.pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "postgres": err.Error()})
			return
		}
	}
	if a.cache != nil {
		if err := a.cache.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "redis": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": a.cfg.ServiceName})
}

// InvalidateSeedCache drops the shared seed snapshot in Redis. Call it from
// any mutating handler after a successful DB commit so GET /api/v1/seed and
// slice routes cannot return stale data.
func (a *API) InvalidateSeedCache(ctx context.Context) {
	if err := a.cache.InvalidateSeedPayload(ctx); err != nil {
		log.Printf("redis invalidate seed: %v", err)
	}
}

// loadCached returns the full seed payload, using the same Redis entry as GET /seed
// so list routes stay consistent and avoid hammering Postgres on every slice request.
func (a *API) loadCached(c *gin.Context) (*models.SeedData, bool) {
	var cached models.SeedData
	ok, err := a.cache.GetJSON(c.Request.Context(), cache.KeySeedPayloadV1, &cached)
	if err != nil {
		log.Printf("redis get seed: %v", err)
	}
	if ok {
		return &cached, true
	}

	data, err := a.seed.Load(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	if err := a.cache.SetJSON(c.Request.Context(), cache.KeySeedPayloadV1, data, a.cfg.SeedCacheTTL); err != nil {
		log.Printf("redis set seed: %v", err)
	}
	return data, true
}

func (a *API) getSeed(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d)
}

func (a *API) listVendors(c *gin.Context) {
	if limit, offset, q, ok := parsePage(c); ok && a.procurement != nil {
		rows, err := a.procurement.ListVendors(c.Request.Context(), limit, offset, q)
		if mapProcurementErr(c, err) {
			return
		}
		c.JSON(http.StatusOK, rows)
		return
	}
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Vendors)
}

func (a *API) listItems(c *gin.Context) {
	if limit, offset, q, ok := parsePage(c); ok && a.procurement != nil {
		rows, err := a.procurement.ListItems(c.Request.Context(), limit, offset, q)
		if mapProcurementErr(c, err) {
			return
		}
		c.JSON(http.StatusOK, rows)
		return
	}
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Items)
}

func (a *API) listBudgets(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Budgets)
}

func (a *API) listRequisitions(c *gin.Context) {
	if limit, offset, q, ok := parsePage(c); ok && a.procurement != nil {
		rows, err := a.procurement.ListRequisitions(c.Request.Context(), limit, offset, q)
		if mapProcurementErr(c, err) {
			return
		}
		c.JSON(http.StatusOK, rows)
		return
	}
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Requisitions)
}

func (a *API) listRfqs(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Rfqs)
}

func (a *API) listPOs(c *gin.Context) {
	if limit, offset, q, ok := parsePage(c); ok && a.procurement != nil {
		rows, err := a.procurement.ListPurchaseOrders(c.Request.Context(), limit, offset, q)
		if mapProcurementErr(c, err) {
			return
		}
		c.JSON(http.StatusOK, rows)
		return
	}
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Pos)
}

func (a *API) listGrns(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Grns)
}

func (a *API) listInvoices(c *gin.Context) {
	if limit, offset, q, ok := parsePage(c); ok && a.procurement != nil {
		rows, err := a.procurement.ListInvoices(c.Request.Context(), limit, offset, q)
		if mapProcurementErr(c, err) {
			return
		}
		c.JSON(http.StatusOK, rows)
		return
	}
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Invoices)
}

func (a *API) listContracts(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Contracts)
}

func (a *API) listPayments(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Payments)
}

func (a *API) listAudit(c *gin.Context) {
	d, ok := a.loadCached(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, d.Audit)
}
