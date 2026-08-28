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

	files := migrationFiles()
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

// migrationFiles is the ordered list the runner applies. Order is the schema's
// history and versions are positional (index+1), so a new migration is ALWAYS
// appended — inserting one renumbers every migration after it and a database
// that has already run them would skip the newcomer while re-running nothing.
//
// TestEveryMigrationFileIsApplied fails the build if a file in the migrations
// directory is missing from this list.
func migrationFiles() []string {
	return []string{
		"001_schema.sql", "002_data.sql", "003_notifications.sql", "004_rbac.sql",
		"005_procurement_mutations.sql", "006_procurement_extended_writes.sql",
		"007_rbac_admin_write_grants.sql", "008_staff.sql", "009_pm_integration.sql",
		"010_drop_dead_tables.sql", "011_party_portal.sql", "012_scm_party_link.sql",
		"013_requisition_integration.sql", "014_procurement_controls.sql",
		"015_budget_accrual.sql",
		// Appended (not inserted) to avoid renumbering existing versions: this
		// idempotent ALTER was orphaned by a 010_* filename collision, so fresh
		// DBs never got requisitions.pm_workspace_owner. Safe to re-run.
		"010_pm_workspace_owner.sql",
		"016_procurement_request_intake.sql",
		"017_fuel_catalogue.sql",
		"018_requisition_approval_tiers.sql",
		"019_receiving_payments.sql",
		// 020–022 were written, committed and embedded by the //go:embed *.sql
		// directive, but never added here — and this list, not the directory, is
		// what the runner reads. They had therefore never been applied to any
		// database: the desk chain of ADR 0002 (removing the money desk, giving
		// the chain its own terminal, requiring a reason to cancel someone
		// else's request) existed as SQL and as an accepted decision record, and
		// not in the schema. Only the tests referenced the files, and they read
		// the SQL text directly rather than running it, so nothing noticed.
		//
		// TestEveryMigrationIsApplied now fails if a .sql file is added to the
		// directory without being listed here.
		"020_requisition_desk_chain.sql",
		"021_requisition_terminal_is_authorization.sql",
		"022_commitment_chain_ends_at_commitment.sql",
		"023_item_stockable.sql",
		"024_search_trigram_indexes.sql",
		"025_purge_demo_seed.sql",
		"026_external_refs.sql",
		"027_uuid_entity_ids.sql",
		"028_monolith_recurring_invoices.sql",
		"029_invoice_grn_fk.sql",
	}
}

func execSQL(ctx context.Context, tx pgx.Tx, sql string) error {
	sql = strings.TrimSpace(strings.ReplaceAll(sql, "\r\n", "\n"))
	if sql == "" {
		return nil
	}
	for _, chunk := range splitStatements(sql) {
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

// splitStatements splits a migration into chunks on a ";" followed by a blank
// line, but never inside a dollar-quoted block.
//
// The previous strings.Split(sql, ";\n\n") had no notion of quoting, so a
// DO $tag$ ... $tag$ body containing a statement followed by a blank line -
// ordinary formatting inside PL/pgSQL - was cut in half and both halves sent as
// invalid SQL. MES 008_machine_telemetry_policies could never be applied for exactly
// that reason: its "END IF;" is followed by a blank line, and Postgres rejected
// the fragment with "unterminated dollar-quoted string".
func splitStatements(sql string) []string {
	var out []string
	start := 0
	tag := "" // the open dollar-quote tag, empty when outside one
	for i := 0; i < len(sql); i++ {
		if tag != "" {
			if sql[i] == '$' && strings.HasPrefix(sql[i:], tag) {
				i += len(tag) - 1
				tag = ""
			}
			continue
		}
		if sql[i] == '$' {
			if t := dollarTagAt(sql[i:]); t != "" {
				tag = t
				i += len(t) - 1
				continue
			}
		}
		if sql[i] == ';' && strings.HasPrefix(sql[i:], ";\n\n") {
			out = append(out, sql[start:i+1])
			start = i + 1
		}
	}
	return append(out, sql[start:])
}

// dollarTagAt returns the dollar-quote tag opening at s (e.g. "$$" or "$body$"),
// or "" if s does not open one.
func dollarTagAt(s string) string {
	j := 1
	for j < len(s) {
		c := s[j]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			j++
			continue
		}
		break
	}
	if j < len(s) && s[j] == '$' {
		return s[:j+1]
	}
	return ""
}
