package middleware

import (
	"log"

	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/auditlog"
)

// RequestAudit records one row per HTTP request after the handler runs.
func RequestAudit(store *auditlog.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		var uidPtr *int64
		mode := AuthMode(c)
		if mode != "gateway" && mode != "jwt" {
			if v, ok := c.Get(CtxUserID); ok {
				if id, ok := v.(int64); ok {
					uidPtr = &id
				}
			}
		}
		actor := "anonymous"
		if v, ok := c.Get(CtxEmail); ok {
			if s, ok := v.(string); ok && s != "" {
				actor = s
			}
		}
		if actor == "anonymous" {
			if id, ok := UserID(c); ok {
				actor = id.String()
			}
		}
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		entry := auditlog.Entry{
			UserID:       uidPtr,
			ActorEmail:   actor,
			Action:       "http." + c.Request.Method,
			ResourceType: "http",
			ResourceID:   path,
			Method:       c.Request.Method,
			Path:         path,
			StatusCode:   c.Writer.Status(),
			IP:           c.ClientIP(),
			UserAgent:    c.Request.UserAgent(),
		}
		if err := store.Insert(c.Request.Context(), entry); err != nil {
			log.Printf("audit log insert: %v", err)
		}
	}
}
