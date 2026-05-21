package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/repo"
)

func ctxActorUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get(middleware.CtxUserID)
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	default:
		return 0, false
	}
}

func mapRBACWriteErr(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repo.ErrInvalidArgument) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return true
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid reference", "detail": pe.Message})
		return true
	}
	if errors.As(err, &pe) && pe.Code == "23505" {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate key", "detail": pe.Message})
		return true
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	return true
}

const adminPasswordMinRunes = 8

func validateAdminPassword(pw string) error {
	if strings.TrimSpace(pw) != pw {
		return fmt.Errorf("%w: password must not have leading or trailing whitespace", repo.ErrInvalidArgument)
	}
	if utf8.RuneCountInString(pw) < adminPasswordMinRunes {
		return fmt.Errorf("%w: password must be at least %d characters", repo.ErrInvalidArgument, adminPasswordMinRunes)
	}
	return nil
}

type patchAdminUserBody struct {
	IsActive    *bool `json:"isActive"`
	IsSuperuser *bool `json:"isSuperuser"`
}

func (a *API) patchAdminUser(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	var body patchAdminUserBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.IsActive == nil && body.IsSuperuser == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no changes"})
		return
	}
	target, err := a.rbac.GetUserByID(c.Request.Context(), id)
	if err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	actorID, ok := ctxActorUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing actor"})
		return
	}
	if body.IsActive != nil && !*body.IsActive && id == actorID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot deactivate your own account"})
		return
	}
	if body.IsSuperuser != nil && !*body.IsSuperuser && id == actorID && target.IsSuperuser {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot remove your own superuser flag"})
		return
	}
	if err := a.rbac.UpdateUserFlags(c.Request.Context(), id, body.IsActive, body.IsSuperuser); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type putAdminUserGroupsBody struct {
	GroupIDs []int64 `json:"groupIds"`
}

func (a *API) putAdminUserGroups(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	var body putAdminUserGroupsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	target, err := a.rbac.GetUserByID(c.Request.Context(), id)
	if err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if err := a.rbac.ReplaceUserGroups(c.Request.Context(), id, body.GroupIDs); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type putGroupPermissionsBody struct {
	PermissionIDs []int64 `json:"permissionIds"`
}

func (a *API) putGroupPermissions(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	gid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || gid < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body putGroupPermissionsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ok, err := a.rbac.GroupExists(c.Request.Context(), gid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	if err := a.rbac.ReplaceGroupPermissions(c.Request.Context(), gid, body.PermissionIDs); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type postAdminCreateUserBody struct {
	Email       string  `json:"email" binding:"required"`
	Password    string  `json:"password" binding:"required"`
	IsSuperuser bool    `json:"isSuperuser"`
	IsActive    *bool   `json:"isActive"`
	GroupIDs    []int64 `json:"groupIds"`
}

func (a *API) postAdminCreateUser(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	var body postAdminCreateUserBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
		return
	}
	if err := validateAdminPassword(body.Password); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	hashB, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	id, err := a.rbac.CreateUserWithGroups(c.Request.Context(), email, string(hashB), body.IsSuperuser, active, body.GroupIDs)
	if mapRBACWriteErr(c, err) {
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":          id,
		"email":       email,
		"isSuperuser": body.IsSuperuser,
		"isActive":    active,
	})
}

type patchAdminUserPasswordBody struct {
	Password string `json:"password" binding:"required"`
}

func (a *API) patchAdminUserPassword(c *gin.Context) {
	if a.rbac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "rbac not configured"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	var body patchAdminUserPasswordBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateAdminPassword(body.Password); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	target, err := a.rbac.GetUserByID(c.Request.Context(), id)
	if err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	hashB, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := a.rbac.UpdateUserPasswordHash(c.Request.Context(), id, string(hashB)); err != nil {
		mapRBACWriteErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
