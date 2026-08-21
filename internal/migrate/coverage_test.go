package migrate

import (
	"sort"
	"testing"

	appmigrations "iag-procurement/backend/migrations"
)

// The runner applies the files named in migrationFiles(), in that order. The
// //go:embed *.sql directive, by contrast, picks up everything in the directory.
// Those two lists silently disagreed for three migrations: 020, 021 and 022 were
// written, committed and embedded, but never listed — so they had never been
// applied to any database. ADR 0002's approval-chain changes existed as SQL and
// as an accepted decision record, and not in the schema.
//
// Nothing caught it because the only code referencing those files was a test
// that read their SQL text to assert on it, rather than running them.
func TestEveryMigrationFileIsApplied(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range migrationFiles() {
		if listed[name] {
			t.Errorf("%s is listed twice; it would be applied under two versions", name)
		}
		listed[name] = true
	}

	entries, err := appmigrations.Files.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var missing []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !listed[e.Name()] {
			missing = append(missing, e.Name())
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these migrations are embedded but never applied — add them to the end of the list "+
			"in migrate.go (append, never insert: versions are positional): %v", missing)
	}
}

// Every listed file must exist, or the runner fails at startup on a fresh
// database — after having already applied the migrations before it.
func TestEveryListedMigrationExists(t *testing.T) {
	for _, name := range migrationFiles() {
		if _, err := appmigrations.Files.ReadFile(name); err != nil {
			t.Errorf("migration %q is listed but not embedded: %v", name, err)
		}
	}
}
