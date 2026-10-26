//go:build integration

package sqlite_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// This opt-in storage comparison is not an admission/worker benchmark. Both
// layouts perform 200 single-entry transactions and decode all retained detail
// documents twice (first read then warm read), with real file-backed SQLite.
//
//nolint:paralleltest // Alternating samples must not compete for SQLite or CPU resources.
func TestDefinitionRevisionStorageComparison(t *testing.T) {
	if os.Getenv("HEARTH_REVISION_STORAGE") != "1" {
		t.Skip("set HEARTH_REVISION_STORAGE=1 for the file-backed storage comparison")
	}
	for _, workload := range []struct {
		name        string
		perRevision int
		skips       bool
	}{
		{"many_runs", 200, false}, {"frequent_edits", 1, false}, {"skip_only", 1, true},
	} {
		for sample := range 5 {
			for _, shared := range []bool{sample%2 == 0, sample%2 != 0} {
				t.Run(fmt.Sprintf("%s/sample%d/shared%t", workload.name, sample, shared), func(t *testing.T) {
					revisionStorageSample(t, workload.perRevision, workload.skips, shared)
				})
			}
		}
	}
}

//nolint:gocognit // Keep both layouts' ordered write/read/accounting sequence visible in this opt-in measurement.
func revisionStorageSample(t *testing.T, perRevision int, skips, shared bool) {
	t.Helper()
	database, provider := revisionMigrationDatabase(t)
	if shared {
		if _, err := provider.UpTo(t.Context(), 21); err != nil {
			t.Fatal(err)
		}
	}
	id := newAutomationIDString(t)
	definition := validDomainDefinition(t)
	definition.Steps[0].Body = automations.CommandStep{
		EntityID:      newEntityID(t),
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true,"padding":"` + strings.Repeat("x", 32*1024) + `"}`),
	}
	definition.Conditions = conditionLeaf("admission", newEntityID(t), automations.ComparisonEqual, "true")
	decision := automations.BypassedDecision(*definition.Conditions)
	if skips {
		decision = automations.NotEvaluatedDecision(*definition.Conditions)
	}
	var artifact string
	started := time.Now()
	for index := range 200 {
		revision := index/perRevision + 1
		definition.Name = fmt.Sprintf("Revision %d", revision)
		var rawDefinition json.RawMessage
		var err error
		// The baseline serialized the full snapshot for every Run; the new path
		// only serializes on definition writes. Both leave Skips inline.
		if index%perRevision == 0 || (!shared && !skips) {
			rawDefinition, err = automations.EncodeDefinition(definition)
			if err != nil {
				t.Fatal(err)
			}
			artifact = string(rawDefinition)
		}
		var rawDecision json.RawMessage
		if shared && !skips {
			rawDecision, err = automations.EncodePreparedConditionDecisionEvidence(decision)
		} else {
			rawDecision, err = automations.EncodeConditionDecision(decision)
		}
		if err != nil {
			t.Fatal(err)
		}
		tx, err := database.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if index%perRevision == 0 {
			writeStorageRevision(t, tx, id, revision, artifact, shared)
		}
		if skips {
			_, err = tx.ExecContext(t.Context(), `INSERT INTO automation_history (
				id, automation_id, automation_name, kind, revision, recorded_at, skip_source, skip_reason,
				skip_matched_triggers_json, fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
				fact_value_json, fact_emitted_at, condition_decision_json)
				VALUES (?, ?, 'Storage', 'skip', ?, ?, 'device_fact', 'automation_busy', ?, ?, 'observation', ?, 'applied', ?, 'true', ?, ?)`,
				newSkipIDString(t), id, revision, migrationTimestamp, matchedTriggerJSON(t), newFactIDString(t),
				string(newEntityID(t)), newObservationIDString(t), migrationTimestamp, string(rawDecision))
		} else {
			configurationColumn, configuration := "run_snapshot_json", any(artifact)
			if shared {
				configurationColumn, configuration = "definition_revision", revision
			}
			_, err = tx.ExecContext(
				t.Context(),
				`INSERT INTO automation_history (
				id, automation_id, automation_name, kind, revision, recorded_at, `+configurationColumn+`,
				run_source, run_status, run_started_at, run_completed_at, run_matched_trigger_ids_json,
				condition_decision_json) VALUES (?, ?, 'Storage', 'run', ?, ?, ?, 'manual', 'succeeded', ?, ?, '[]', ?)`,
				newRunIDString(
					t,
				),
				id,
				revision,
				migrationTimestamp,
				configuration,
				migrationTimestamp,
				migrationTimestamp,
				string(rawDecision),
			)
		}
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	writeTime := time.Since(started)
	// Model removal of the current head after all revisions have been used.
	mustExec(t, database, `DELETE FROM automations`)
	if shared {
		mustExec(t, database, `DELETE FROM automation_definition_revisions AS artifact WHERE NOT EXISTS (
		SELECT 1 FROM automation_history AS history WHERE history.automation_id = artifact.automation_id AND history.definition_revision = artifact.revision)`)
	}
	var retainedBytes int64
	payloadQuery := `SELECT coalesce(sum(length(CAST(run_snapshot_json AS BLOB))), 0) FROM automation_history`
	if shared {
		payloadQuery = `SELECT coalesce(sum(length(CAST(definition_json AS BLOB))), 0) FROM automation_definition_revisions`
	}
	if err := database.QueryRow(payloadQuery).Scan(&retainedBytes); err != nil {
		t.Fatal(err)
	}
	var evidenceBytes int64
	if err := database.QueryRow(`SELECT sum(length(CAST(condition_decision_json AS BLOB)) + coalesce(length(CAST(skip_matched_triggers_json AS BLOB)), 0)) FROM automation_history`).
		Scan(&evidenceBytes); err != nil {
		t.Fatal(err)
	}
	firstRead := readRevisionStorageDetails(t, database, skips, shared)
	warmRead := readRevisionStorageDetails(t, database, skips, shared)
	mustExec(t, database, `PRAGMA wal_checkpoint(TRUNCATE)`)
	var pageCount, pageSize int64
	if err := database.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"entries=200 per_revision=%d skips=%t shared=%t payload_bytes=%d allocated_page_bytes=%d write=%s first_read=%s warm_read=%s",
		perRevision,
		skips,
		shared,
		retainedBytes+evidenceBytes,
		pageCount*pageSize,
		writeTime,
		firstRead,
		warmRead,
	)
}

func writeStorageRevision(t *testing.T, tx *sql.Tx, id string, revision int, artifact string, shared bool) {
	t.Helper()
	var err error
	if shared {
		_, err = tx.ExecContext(
			t.Context(),
			`INSERT INTO automation_definition_revisions VALUES (?, ?, ?)`,
			id,
			revision,
			artifact,
		)
		if err == nil {
			_, err = tx.ExecContext(t.Context(), `INSERT INTO automations VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET revision = excluded.revision`, id, revision, migrationTimestamp, migrationTimestamp)
		}
	} else {
		_, err = tx.ExecContext(t.Context(), `INSERT INTO automations VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET revision = excluded.revision, definition_json = excluded.definition_json`, id, revision, artifact, migrationTimestamp, migrationTimestamp)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
}

func readRevisionStorageDetails(t *testing.T, database *sql.DB, skips, shared bool) time.Duration {
	t.Helper()
	query := `SELECT run_snapshot_json, condition_decision_json FROM automation_history ORDER BY id`
	if shared {
		query = `SELECT artifact.definition_json, history.condition_decision_json FROM automation_history AS history
		JOIN automation_definition_revisions AS artifact ON artifact.automation_id = history.automation_id AND artifact.revision = history.definition_revision ORDER BY history.id`
	}
	if skips {
		query = `SELECT NULL, condition_decision_json FROM automation_history ORDER BY id`
	}
	started := time.Now()
	rows, err := database.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var snapshot sql.NullString
		var decision string
		if err = rows.Scan(&snapshot, &decision); err != nil {
			t.Fatal(err)
		}
		var conditions *automations.Condition
		if !skips {
			definition, decodeErr := automations.DecodeDefinition(json.RawMessage(snapshot.String))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			conditions = definition.Conditions
		}
		if shared && !skips {
			_, err = automations.DecodeConditionDecisionEvidence(json.RawMessage(decision), conditions)
		} else {
			_, err = automations.DecodeConditionDecision(json.RawMessage(decision))
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Fatalf("decoded %d entries, want 200", count)
	}
	return time.Since(started)
}
