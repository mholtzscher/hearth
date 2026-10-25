//go:build integration

package db //nolint:testpackage // Verifies transactional corruption rejection at the embedded migration boundary.

import (
	"path/filepath"
	"testing"
)

func TestExpressionMigrationRejectsMalformedNestedArmsAtomically(t *testing.T) {
	t.Parallel()
	for name, steps := range map[string]string{
		"empty then":            `[{"id":"if","kind":"if","conditions":{},"then":[]}]`,
		"missing branch steps":  `[{"id":"choose","kind":"choose","branches":[{}]}]`,
		"null else":             `[{"id":"if","kind":"if","conditions":{},"then":[{"id":"d","kind":"delay","duration_ms":1}],"else":null}]`,
		"object default":        `[{"id":"choose","kind":"choose","branches":[{"id":"b","conditions":{},"steps":[{"id":"d","kind":"delay","duration_ms":1}]}],"default":{}}]`,
		"empty nested sequence": `[{"id":"if","kind":"if","conditions":{},"then":[{"id":"choose","kind":"choose","branches":[]}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, err := Open(t.Context(), filepath.Join(t.TempDir(), "corrupt.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			provider := newGooseProvider(t, database)
			if _, err = provider.UpTo(t.Context(), 19); err != nil {
				t.Fatal(err)
			}
			document := `{"name":"corrupt","enabled":true,"triggers":[],"steps":` + steps + `}`
			insertExpressionLegacyParents(t, database, document)
			if _, err = database.ExecContext(
				t.Context(),
				`INSERT INTO automation_run_steps(run_id,position,step_id,status) VALUES (?,0,'preserved','not_attempted')`,
				expressionMigrationRun,
			); err != nil {
				t.Fatal(err)
			}
			before := expressionChildRows(t, database)
			if _, err = provider.UpTo(t.Context(), 20); err == nil {
				t.Fatal("corrupt nested arm migrated")
			}
			var stored string
			if err = database.QueryRowContext(t.Context(), `SELECT definition_json FROM automations`).
				Scan(&stored); err != nil ||
				stored != document {
				t.Fatal("failed migration changed parent")
			}
			if after := expressionChildRows(t, database); after != before {
				t.Fatalf("failed migration changed children: %s", after)
			}
			assertColumnAbsent(t.Context(), t, database, "automation_history", "data_evidence")
			assertNoForeignKeyViolations(t.Context(), t, database)
		})
	}
}
