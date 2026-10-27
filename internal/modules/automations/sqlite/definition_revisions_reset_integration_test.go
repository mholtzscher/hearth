//go:build integration

package sqlite_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestDefinitionRevisionMigrationResetsOnlyAutomationData(t *testing.T) {
	t.Parallel()
	database, provider := revisionMigrationDatabase(t)
	automationTables := seedAutomationResetRows(t, database)
	seedOtherModuleResetRows(t, database)
	otherTables := snapshotOtherModuleRows(t, database)
	if _, err := provider.UpTo(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	for _, table := range append(automationTables, "automation_fact_receipts", "automation_schedule_watermarks", "automation_definition_revisions") {
		if count := rowCount(t, database, table); count != 0 {
			t.Fatalf("reset retained %d rows in %s", count, table)
		}
	}
	for _, table := range otherTables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	assertMigrationRowsUnchanged(t, database, "nonautomation_schema")
	assertScheduleMigrationIntegrity(t, database)
	// Exercise the current repository only after all current migrations.
	if _, err := provider.UpTo(t.Context(), 22); err != nil {
		t.Fatal(err)
	}
	if err := platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	retained := historyEntry(t, repository, record.ID, string(run.ID)).(automations.Run)
	if retained.Revision != 1 || retained.Snapshot.Name != record.Definition.Name || len(retained.Executions) != 0 {
		t.Fatalf("post-reset Run = %#v", retained)
	}
	if err := platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAutomation(t.Context(), record.ID); err != nil {
		t.Fatalf("repeat startup reset new definitions: %v", err)
	}
	if count := rowCount(t, database, "automation_history"); count != 1 {
		t.Fatalf("repeat startup history = %d", count)
	}
	currentProvider, err := platformdb.NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, downErr := currentProvider.Down(
		t.Context(),
	); downErr == nil ||
		!strings.Contains(downErr.Error(), "pre-upgrade backup") {
		t.Fatalf("unsafe Down = %v", downErr)
	}
}

func TestDefinitionRevisionResetRollsBackOnFailure(t *testing.T) {
	t.Parallel()
	database, provider := revisionMigrationDatabase(t)
	tables := seedAutomationResetRows(t, database)
	for _, table := range []string{"automation_fact_receipts", "automation_schedule_watermarks"} {
		//nolint:unqueryvet // Snapshot all receipt and schedule columns before the failed reset.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
		tables = append(tables, table)
	}
	// Force schema creation to fail after dropping both old parents and cascading
	// deletion of child rows. The transaction must restore the old tables and data.
	mustExec(t, database, `CREATE TABLE automation_definition_revisions (collision TEXT)`)
	if _, err := provider.UpTo(t.Context(), 21); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("reset error = %v", err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	var version int64
	if err := database.QueryRow(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).
		Scan(&version); err != nil ||
		version != 20 {
		t.Fatalf("failed reset version = %d, %v", version, err)
	}
	mustExec(t, database, `DROP TABLE automation_definition_revisions`)
	if _, err := provider.UpTo(t.Context(), 21); err != nil {
		t.Fatalf("reset after fixing failure: %v", err)
	}
	assertScheduleMigrationIntegrity(t, database)
}

func snapshotOtherModuleRows(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'
		AND name NOT LIKE 'automation%' AND name NOT LIKE 'goose%' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		//nolint:unqueryvet // Exact whole-row comparison checks every unrelated retained column.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
	}
	mustExec(t, database, `CREATE TEMP VIEW nonautomation_schema AS
		SELECT type, name, tbl_name, sql FROM sqlite_schema WHERE tbl_name NOT LIKE 'automation%'
		AND name NOT LIKE 'goose%' AND name NOT LIKE 'sqlite_%'`)
	//nolint:unqueryvet // Preserve unrelated tables, indexes, and triggers as well as their data.
	mustExec(t, database, `CREATE TEMP TABLE expected_nonautomation_schema AS SELECT * FROM nonautomation_schema`)
	return tables
}
