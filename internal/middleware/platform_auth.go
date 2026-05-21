package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-authclient"
	"iag-procurement/backend/internal/ctxkeys"
)

const (
	HeaderUserID        = "X-IAG-User-Id"
	HeaderEmail         = "X-IAG-Email"
	HeaderGroups        = "X-IAG-Groups"
	HeaderRoles         = "X-IAG-Roles"
	HeaderPermissions   = "X-IAG-Permissions"
	HeaderIsSuperuser   = "X-IAG-Is-Superuser"
	HeaderIsStaff       = "X-IAG-Is-Staff"
	HeaderGatewaySecret = "X-IAG-Gateway-Secret"
)

type PlatformAuth struct {
	authMode      string
	gatewaySecret string
	verifier      *authclient.Verifier
}

type PlatformAuthOptions struct {
	Mode          string
	GatewaySecret string
	Verifier      *authclient.Verifier
}

func NewPlatformAuth(opts PlatformAuthOptions) *PlatformAuth {
	return &PlatformAuth{
		authMode:      opts.Mode,
		gatewaySecret: opts.GatewaySecret,
		verifier:      opts.Verifier,
	}
}

func SetAuthMode(c *gin.Context, mode string) {
	c.Set(ctxkeys.AuthMode, mode)
}

func AuthMode(c *gin.Context) string {
	v, _ := c.Get(ctxkeys.AuthMode)
	s, _ := v.(string)
	return s
}

func isPublicProbePath(path string) bool {
	switch path {
	case "/health", "/healthz", "/ready":
		return true
	default:
		return false
	}
}

func (m *PlatformAuth) AttachPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		SetAuthMode(c, m.authMode)
		if isPublicProbePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		switch m.authMode {
		case "gateway":
			m.fromGateway(c)
		case "jwt":
			m.fromJWT(c)
		default:
			c.Next()
		}
	}
}

func (m *PlatformAuth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := AuthMode(c)
		if mode == "legacy" || mode == "" {
			c.Next()
			return
		}
		if _, ok := UserID(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Next()
	}
}

func (m *PlatformAuth) fromGateway(c *gin.Context) {
	if m.gatewaySecret != "" && c.GetHeader(HeaderGatewaySecret) != m.gatewaySecret {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	sub := c.GetHeader(HeaderUserID)
	if sub == "" {
		c.Next()
		return
	}
	userID, err := uuid.Parse(sub)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid user id"})
		return
	}
	groups := splitHeaderList(c.GetHeader(HeaderGroups))
	if len(groups) == 0 {
		groups = splitHeaderList(c.GetHeader(HeaderRoles))
	}
	perms := splitHeaderList(c.GetHeader(HeaderPermissions))
	claims := &authclient.Claims{
		Email:       c.GetHeader(HeaderEmail),
		IsSuperuser: strings.EqualFold(c.GetHeader(HeaderIsSuperuser), "true"),
		IsStaff:     strings.EqualFold(c.GetHeader(HeaderIsStaff), "true"),
		Groups:      groups,
		Roles:       groups,
		Permissions: perms,
	}
	claims.Subject = sub
	setPrincipal(c, userID, claims, perms)
	c.Next()
}

func (m *PlatformAuth) fromJWT(c *gin.Context) {
	if m.verifier == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "jwt verifier not configured"})
		return
	}
	tokenStr := bearerToken(c)
	if tokenStr == "" {
		c.Next()
		return
	}
	claims, userID, err := m.verifier.Verify(tokenStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	setPrincipal(c, userID, claims, claims.Permissions)
	c.Next()
}

func setPrincipal(c *gin.Context, userID uuid.UUID, claims *authclient.Claims, perms []string) {
	c.Set(ctxkeys.UserID, userID)
	c.Set(ctxkeys.Claims, claims)
	c.Set(ctxkeys.Permissions, perms)
	c.Set(CtxEmail, claims.Email)
	c.Set(CtxSuper, claims.IsSuperuser)
	c.Set(CtxPerms, perms)
}

func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxkeys.UserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func PlatformClaims(c *gin.Context) (*authclient.Claims, bool) {
	v, ok := c.Get(ctxkeys.Claims)
	if !ok {
		return nil, false
	}
	cl, ok := v.(*authclient.Claims)
	return cl, ok
}

func PlatformPermissions(c *gin.Context) []string {
	v, ok := c.Get(ctxkeys.Permissions)
	if !ok {
		return nil
	}
	list, _ := v.([]string)
	return list
}

func bearerToken(c *gin.Context) string {
	if q := strings.TrimSpace(c.Query("token")); q != "" {
		return q
	}
	header := c.GetHeader("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

func splitHeaderList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// VerifyBearerToken validates a raw JWT (reserved for future WebSocket use).
func (m *PlatformAuth) VerifyBearerToken(tokenStr string) (uuid.UUID, *authclient.Claims, error) {
	if m.verifier == nil {
		return uuid.Nil, nil, fmt.Errorf("jwt verifier not configured")
	}
	claims, userID, err := m.verifier.Verify(tokenStr)
	if err != nil {
		return uuid.Nil, nil, err
	}
	return userID, claims, nil
}
