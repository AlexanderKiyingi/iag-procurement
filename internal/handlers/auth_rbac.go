package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alvor-technologies/iag-platform-go/apierr"
	"iag-procurement/backend/internal/middleware"
)

type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) postLogin(c *gin.Context) {
	if a.iam == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "authentication not configured"})
		return
	}
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil || body.Email == "" || body.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and password required"})
		return
	}
	res, err := a.iam.Login(c.Request.Context(), body.Email, body.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (a *API) getMe(c *gin.Context) {
	mode := middleware.AuthMode(c)
	if mode == "gateway" || mode == "jwt" {
		uid, ok := middleware.UserID(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		claims, _ := middleware.PlatformClaims(c)
		email := ""
		super := false
		if claims != nil {
			email = claims.Email
			super = claims.IsSuperuser
		}
		perms := middleware.PlatformPermissions(c)
		if perms == nil {
			perms = []string{}
		}
		c.JSON(http.StatusOK, gin.H{
			"userId":      uid.String(),
			"email":       email,
			"isSuperuser": super,
			"permissions": perms,
		})
		return
	}
	uid, _ := c.Get(middleware.CtxUserID)
	email, _ := c.Get(middleware.CtxEmail)
	super, _ := c.Get(middleware.CtxSuper)
	perms, _ := c.Get(middleware.CtxPerms)
	list, _ := perms.([]string)
	if list == nil {
		list = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"userId":      uid,
		"email":       email,
		"isSuperuser": super,
		"permissions": list,
	})
}

func (a *API) listAdminPermissions(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	rows, err := a.rbac.ListPermissions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (a *API) listAdminGroups(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	rows, err := a.rbac.ListGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (a *API) listAdminUsers(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	rows, err := a.rbac.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (a *API) listAPIAuditLogs(c *gin.Context) {
	if a.audit == nil {
		apierr.JSON(c, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "audit log not configured")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	rows, err := a.audit.List(c.Request.Context(), limit)
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.CodeInternal, "could not list audit logs")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": len(rows)})
}
