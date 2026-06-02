package middleware

import (
	"net/http"
	"strings"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/iam"
)

const (
	CtxUserID = "auth_user_id" // legacy local user id (int64)
	CtxEmail  = "auth_email"
	CtxSuper  = "auth_super"
	CtxPerms  = "auth_perms"
)

// JWTAuth validates procurement-local JWT (AUTH_MODE=legacy only).
func JWTAuth(svc *iam.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Bearer") {
			apierr.Unauthorized(c, "missing bearer token")
			return
		}
		raw := strings.TrimSpace(parts[1])
		if raw == "" {
			apierr.Unauthorized(c, "missing bearer token")
			return
		}
		cl, err := svc.ParseToken(raw)
		if err != nil {
			apierr.Unauthorized(c, "invalid or expired token")
			return
		}
		c.Set(CtxUserID, cl.UserID)
		c.Set(CtxEmail, cl.Email)
		c.Set(CtxSuper, cl.IsSuperuser)
		c.Set(CtxPerms, cl.Permissions)
		c.Next()
	}
}

// RequirePermission checks platform JWT permissions (gateway/jwt) or legacy local RBAC.
func RequirePermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := AuthMode(c)
		if mode == "gateway" || mode == "jwt" {
			if claims, ok := PlatformClaims(c); ok && authclient.HasPermission(claims, code) {
				c.Next()
				return
			}
			apierr.WriteWith(c, http.StatusForbidden, apierr.CodeForbidden,
				"permission denied: "+code, gin.H{"required_permission": code})
			return
		}
		if v, ok := c.Get(CtxSuper); ok {
			if super, _ := v.(bool); super {
				c.Next()
				return
			}
		}
		v, _ := c.Get(CtxPerms)
		list, _ := v.([]string)
		for _, p := range list {
			if p == code {
				c.Next()
				return
			}
		}
		apierr.WriteWith(c, http.StatusForbidden, apierr.CodeForbidden,
			"permission denied: "+code, gin.H{"required_permission": code})
	}
}
