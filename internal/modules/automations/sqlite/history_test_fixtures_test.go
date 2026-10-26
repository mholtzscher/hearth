//go:build integration

package sqlite_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func insertRetainedHistoryRun(t *testing.T, database *sql.DB, automationID, status string, at time.Time) string {
	t.Helper()
	id := newRunIDString(t)
	var completed, failure any
	if status != "running" {
		completed = migrationTimestamp
	}
	if status == "failed" || status == "interrupted" {
		failure = "core_restarted"
	}
	// History may outlive its head; synthetic Runs still require an artifact.
	mustExec(t, database, `INSERT INTO automation_definition_revisions (automation_id, revision, definition_json)
		SELECT ?, 1, '{}' WHERE NOT EXISTS (
			SELECT 1 FROM automation_definition_revisions WHERE automation_id = ? AND revision = 1)`, automationID, automationID)
	mustExec(t, database, insertHistoryRunSQL, id, automationID, status, failure, completed, "[]")
	mustExec(t, database, `UPDATE automation_history SET recorded_at = ? WHERE id = ?`,
		at.Format("2006-01-02T15:04:05.000000000Z"), id)
	return id
}

func insertRetainedHistorySkip(t *testing.T, database *sql.DB, automationID string, at time.Time) string {
	t.Helper()
	id := newSkipIDString(t)
	stamp := at.Format("2006-01-02T15:04:05.000000000Z")
	mustExec(t, database, insertHistorySkipSQL, id, automationID, stamp,
		newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), "true", stamp,
		matchedTriggerJSON(t), `{"mode":"not_configured","bypass_requested":false}`)
	return id
}

// Seed every retained child table to make whole-row preservation and cascading
// deletion checks non-vacuous. The synthetic evidence needs no external records.
func seedRetainedRunChildren(t *testing.T, database *sql.DB, runID string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO automation_run_steps
		(run_id, step_id, position, status) VALUES (?, 'command', 0, 'not_attempted')`, runID)
	mustExec(t, database, `INSERT INTO automation_run_branch_decisions
		(run_id, step_id, position, decision_json) VALUES (?, 'branch', 0, '{}')`, runID)
	mustExec(t, database, `INSERT INTO automation_run_delays
		(run_id, step_id, position, status, started_at, completed_at)
		VALUES (?, 'wait', 0, 'completed', ?, ?)`, runID, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_notifications
		(run_id, step_id, position, attempt_id, status, started_at, completed_at, outcome)
		VALUES (?, 'notify', 0, ?, 'accepted', ?, ?, 'accepted')`,
		runID, "attempt-"+runID, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_functions
		(run_id, step_id, position, invocation_id, status, started_at, completed_at, catalog_revision, invocation_outcome)
		VALUES (?, 'function', 0, ?, 'succeeded', ?, ?, 'retained-catalog', 'succeeded')`,
		runID, "invocation-"+runID, migrationTimestamp, migrationTimestamp)
}

func seedHistoryRetentionCutoff(t *testing.T, database *sql.DB, cutoff time.Time) []string {
	t.Helper()
	var expired []string
	for range 2 {
		parent := newAutomationIDString(t)
		for _, stamp := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
			for _, status := range []string{"running", "succeeded", "failed", "interrupted"} {
				// Each Automation can have only one running Run.
				automationID := parent
				if status == "running" {
					automationID = newAutomationIDString(t)
				}
				id := insertRetainedHistoryRun(t, database, automationID, status, stamp)
				seedRetainedRunChildren(t, database, id)
				if stamp.Before(cutoff) && status != "running" {
					expired = append(expired, id)
				}
			}
			id := insertRetainedHistorySkip(t, database, parent, stamp)
			if stamp.Before(cutoff) {
				expired = append(expired, id)
			}
		}
	}
	return expired
}

func seedHistoryPagination(
	t *testing.T,
	database *sql.DB,
	parent, other string,
	at time.Time,
) []automations.HistorySummary {
	t.Helper()
	// IDs sort independently of insertion order, with both kinds at each timestamp.
	var expected []automations.HistorySummary
	for _, stamp := range []time.Time{at.Add(time.Nanosecond), at, at.Add(-time.Nanosecond)} {
		for range 3 {
			for _, id := range []string{
				insertRetainedHistoryRun(t, database, parent, "succeeded", stamp),
				insertRetainedHistorySkip(t, database, parent, stamp),
			} {
				expected = append(expected, automations.HistorySummary{ID: id, RecordedAt: stamp})
			}
		}
		insertRetainedHistorySkip(t, database, other, stamp)
	}
	slices.SortFunc(expected, func(a, b automations.HistorySummary) int {
		if order := b.RecordedAt.Compare(a.RecordedAt); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	return expected
}
