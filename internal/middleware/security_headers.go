package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders emits the baseline browser security header set the platform
// applies to every JSON-only API. CSP is strict (no inline scripts, no
// framing, no form submission, no external resource loading) because this
// service does not serve HTML or static assets — every response is JSON.
//
// HSTS is gated on TLS termination (direct TLS or trusted X-Forwarded-Proto)
// so plain-HTTP dev environments (http://localhost) do not lock browsers into
// HTTPS for the developer's whole domain.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")
		c.Header("X-XSS-Protection", "1; mode=block")
		if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		}
		c.Next()
	}
}
