package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema is the Postgres schema this service owns. Setting search_path on
// every connection keeps procurement's tables isolated from any other
// service that shares the same database (notably SCM, which declares its
// own purchase_orders / items in the public schema with incompatible
// column types — a cross-service collision that 502s the migrator
// otherwise).
const Schema = "procurement"

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	// search_path resolves unqualified table names — procurement first, then
	// public as a fallback for any extensions (pg_trgm, uuid-ossp, etc.) that
	// live there.
	cfg.ConnConfig.RuntimeParams["search_path"] = Schema + ", public"
	// Ensure the schema exists on every new connection. Idempotent and runs
	// at most once per pooled connection, not per query.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
