package handlers

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// defaultPageLimit / maxPageLimit bound a paginated list request.
const (
	defaultPageLimit = 50
	maxPageLimit     = 500
)

// parsePage reads optional ?limit/?offset/?q query params. ok is true only when
// at least one is supplied, so callers can keep returning the full cached array
// (backward compatible) when none are present and hit the DB only when the
// client opts in. limit is clamped to [1, maxPageLimit]; offset to >= 0.
func parsePage(c *gin.Context) (limit, offset int, q string, ok bool) {
	ls := strings.TrimSpace(c.Query("limit"))
	os := strings.TrimSpace(c.Query("offset"))
	q = strings.TrimSpace(c.Query("q"))
	ok = ls != "" || os != "" || q != ""

	limit = defaultPageLimit
	if ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if os != "" {
		if n, err := strconv.Atoi(os); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}
