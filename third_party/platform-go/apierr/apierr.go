// Package apierr provides a consistent JSON error envelope for IAG HTTP APIs:
//
//	{"error":{"code":"FORBIDDEN","message":"permission denied: users.admin"}}
//
// Optional detail fields may be merged at the top level alongside "error".
package apierr

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeBadRequest          = "BAD_REQUEST"
	CodeUnauthorized        = "UNAUTHORIZED"
	CodeForbidden           = "FORBIDDEN"
	CodeNotFound            = "NOT_FOUND"
	CodeConflict            = "CONFLICT"
	CodeValidation          = "VALIDATION_ERROR"
	CodeInternal            = "INTERNAL"
	CodeServiceUnavailable  = "SERVICE_UNAVAILABLE"
	CodeTooManyRequests     = "TOO_MANY_REQUESTS"
)

type Detail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Body struct {
	Error Detail `json:"error"`
}

func body(code, message string) Body {
	return Body{Error: Detail{Code: code, Message: message}}
}

func Write(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, body(code, message))
}

func JSON(c *gin.Context, status int, code, message string) {
	c.JSON(status, body(code, message))
}

// WriteWith merges optional detail fields at the top level (e.g. required_permission).
func WriteWith(c *gin.Context, status int, code, message string, extra gin.H) {
	payload := gin.H{"error": gin.H{"code": code, "message": message}}
	for k, v := range extra {
		payload[k] = v
	}
	c.AbortWithStatusJSON(status, payload)
}

func Unauthorized(c *gin.Context, message string) {
	if message == "" {
		message = "authentication required"
	}
	Write(c, http.StatusUnauthorized, CodeUnauthorized, message)
}

func Forbidden(c *gin.Context, message string) {
	if message == "" {
		message = "access denied"
	}
	Write(c, http.StatusForbidden, CodeForbidden, message)
}

func BadRequest(c *gin.Context, message string) {
	if message == "" {
		message = "invalid request"
	}
	Write(c, http.StatusBadRequest, CodeBadRequest, message)
}

func NotFound(c *gin.Context, message string) {
	if message == "" {
		message = "resource not found"
	}
	Write(c, http.StatusNotFound, CodeNotFound, message)
}

func Internal(c *gin.Context, message string) {
	if message == "" {
		message = "internal server error"
	}
	Write(c, http.StatusInternalServerError, CodeInternal, message)
}
