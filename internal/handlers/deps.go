package handlers

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"iag-procurement/backend/internal/auditlog"
	"iag-procurement/backend/internal/cache"
	"iag-procurement/backend/internal/config"
	"iag-procurement/backend/internal/iam"
	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/rbac"
	"iag-procurement/backend/internal/repo"
	"iag-procurement/backend/internal/signals"
)

// Deps bundles HTTP API dependencies.
type Deps struct {
	Pool        *pgxpool.Pool
	Seed        *repo.Seed
	Procurement *repo.Procurement
	Cache       *cache.Client
	Config      *config.Config
	Notify      *notifications.Service
	Bus         *signals.Bus
	IAM         *iam.Service
	RBAC        *rbac.Store
	Audit        *auditlog.Store
	PlatformAuth *middleware.PlatformAuth
}
