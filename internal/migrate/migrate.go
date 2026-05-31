package migrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	appmigrations "iag-procurement/backend/migrations"
)

// Serializes migrate.Up across concurrent processes so schema/data migrations apply once.
const migrateAdvisoryLockKey1 int32 = 771928834
const migrateAdvisoryLockKey2 int32 = 629471902

// Migration tracking lives in procurement.schema_migrations explicitly so
// it can't collide with any other service that also writes a
// schema_migrations table in the public schema of a shared Postgres
// (notifications, SCM, contract-management all do). search_path is set on
// every pool connection to "procurement, public" by internal/db, so
// unqualified table references in the migration .sql files create their
// objects inside the procurement schema as well — sidestepping the
// cross-service column-type collisions (e.g. SCM's purchase_orders.id is
// UUID, procurement's is TEXT) that previously crashed the migrator.
const migrationTable = `
CREATE TABLE IF NOT EXISTS procurement.schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func Up(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrateAdvisoryLockKey1, migrateAdvisoryLockKey2); err != nil {
		return fmt.Errorf("migrate advisory lock: %w", err)
	}

	if _, err := tx.Exec(ctx, migrationTable); err != nil {
		return fmt.Errorf("migration table: %w", err)
	}

	files := []string{
		"001_schema.sql", "002_data.sql", "003_notifications.sql", "004_rbac.sql",
		"005_procurement_mutations.sql", "006_procurement_extended_writes.sql",
		"007_rbac_admin_write_grants.sql", "008_staff.sql", "009_pm_integration.sql",
	}
	for i, name := range files {
		version := fmt.Sprintf("%d", i+1)
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM procurement.schema_migrations WHERE version = $1)`, version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists {
			continue
		}

		body, err := appmigrations.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := execSQL(ctx, tx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO procurement.schema_migrations (version) VALUES ($1)`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}
	committed = true
	return nil
}

func execSQL(ctx context.Context, tx pgx.Tx, sql string) error {
	sql = strings.TrimSpace(strings.ReplaceAll(sql, "\r\n", "\n"))
	if sql == "" {
		return nil
	}
	for _, chunk := range strings.Split(sql, ";\n\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if _, err := tx.Exec(ctx, chunk); err != nil {
			snippet := chunk
			if len(snippet) > 400 {
				snippet = snippet[:400] + "…"
			}
			return fmt.Errorf("exec migration chunk: %w\n--\n%s", err, snippet)
		}
	}
	return nil
}
