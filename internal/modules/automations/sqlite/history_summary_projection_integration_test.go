//go:build integration

package sqlite_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestHistoryListingProjectsOnlySummaryColumns(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	capture := &historyQueryCapture{DB: database}
	queries := dbsqlc.New(capture)
	for _, continuation := range []bool{false, true} {
		if continuation {
			_, err := queries.ListHistoryAfter(t.Context(), dbsqlc.ListHistoryAfterParams{PageLimit: 1})
			if err != nil {
				t.Fatal(err)
			}
		} else if _, err := queries.ListHistoryFirstPage(t.Context(), dbsqlc.ListHistoryFirstPageParams{Limit: 1}); err != nil {
			t.Fatal(err)
		}
		columns := historyQueryColumns(t, capture)
		want := []string{
			"id", "automation_id", "automation_name", "kind", "revision", "recorded_at",
			"run_status", "run_source", "skip_reason", "skip_source",
			"fact_id", "fact_family", "fact_entity_id", "fact_variant", "fact_causation_id",
			"fact_value_json", "fact_previous_value_json", "fact_emitted_at",
			"hold_trigger_id", "hold_started_at", "hold_due_at",
			"condition_mode", "condition_result", "condition_bypassed",
		}
		if !slices.Equal(columns, want) {
			t.Fatalf("after=%t selected columns = %v, want %v", continuation, columns, want)
		}
	}
}

func historyQueryColumns(t *testing.T, capture *historyQueryCapture) []string {
	t.Helper()
	rows, err := capture.DB.QueryContext(t.Context(), capture.query, capture.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func TestHistoryProjectionPreservesSummaryValuesOnBothPages(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"manual", "observation", "event", "held_state", "schedule"} {
		for _, outcome := range []string{"running", "succeeded", "failed", "interrupted", "automation_busy", "stale_fact", "conditions_false", "conditions_unknown", "bypassed"} {
			if (source == "manual" && (outcome == "automation_busy" || outcome == "stale_fact")) ||
				(source != "manual" && outcome == "bypassed") ||
				((source == "schedule" || source == "held_state") && outcome == "stale_fact") {
				continue
			}
			t.Run(source+"/"+outcome, func(t *testing.T) {
				t.Parallel()
				assertProjectionSummaryPages(t, source, outcome)
			})
		}
	}
}

func assertProjectionSummaryPages(t *testing.T, source, outcome string) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	parent := newAutomationIDString(t)
	want := insertProjectionSummary(t, database, parent, source, outcome)
	params := automations.ListHistoryParams{AutomationID: automations.AutomationID(parent), Limit: 1}
	for _, continuation := range []bool{false, true} {
		if continuation {
			at, cursor := want.RecordedAt.Add(time.Hour), newRunIDString(t)
			params.BeforeRecordedAt, params.BeforeID = &at, &cursor
		}
		page, err := repository.ListHistory(t.Context(), params)
		if err != nil || len(page.Items) != 1 || page.HasMore {
			t.Fatalf("after=%t page = %#v, %v", continuation, page, err)
		}
		if !reflect.DeepEqual(page.Items[0], want) {
			t.Fatalf("after=%t summary = %#v, want %#v", continuation, page.Items[0], want)
		}
	}
}

func insertProjectionSummary(
	t *testing.T,
	database *sql.DB,
	parent, source, outcome string,
) automations.HistorySummary {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, migrationTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	want := automations.HistorySummary{
		AutomationID: automations.AutomationID(parent), AutomationName: "Retained summary", Revision: 9,
		RecordedAt: at, Cause: automations.ManualCause{}, ConditionSummary: automations.NotConfiguredSummary{},
	}
	var factID, family, entity, variant, causation, value, previous, emitted, holdID, start, due any
	switch source {
	case "observation", "event":
		factID, entity, emitted = newFactIDString(t), string(newEntityID(t)), migrationTimestamp
		if source == "observation" {
			family, variant, causation, value, previous = "observation", "applied", newObservationIDString(
				t,
			), `{"level":7}`, `null`
			want.Cause = automations.DeviceFactCause{Fact: automations.ObservationFact{
				FactID: devices.DeviceFactID(factID.(string)), EntityID: devices.EntityID(entity.(string)),
				ObservationID: devices.ObservationID(causation.(string)), Disposition: devices.DispositionApplied,
				Value: devices.Value(`{"level":7}`), PreviousValue: devices.Value(`null`), EmittedAt: at,
			}}
		} else {
			eventID, idErr := devices.NewEntityEventID()
			if idErr != nil {
				t.Fatal(idErr)
			}
			family, variant, causation = "entity_event", "pressed", string(eventID)
			want.Cause = automations.DeviceFactCause{Fact: automations.EntityEventFact{
				FactID: devices.DeviceFactID(factID.(string)), EntityID: devices.EntityID(entity.(string)),
				EventID: eventID, Name: "pressed", EmittedAt: at,
			}}
		}
		source = "device_fact"
	case "held_state":
		holdID, start, due = "held", migrationTimestamp, encodeStoredTimestamp(at.Add(time.Minute))
		want.Cause = automations.HeldStateCause{Evidence: automations.HeldStateEvidence{
			TriggerID: "held", StartedAt: at, DueAt: at.Add(time.Minute),
		}}
	case "schedule":
		want.Cause = automations.ScheduleCause{}
	}
	kind, mode, bypass := "run", "not_configured", 0
	var result, snapshot, runSource, status, failure, started, completed, matched, skipSource, reason, triggers any
	snapshot, runSource, status, started, completed, matched = `{"triggers":[{"id":"cron","kind":"cron"}]}`, source, outcome, migrationTimestamp, migrationTimestamp, `["cron"]`
	want.ID, want.Body = newRunIDString(t), automations.RunHistorySummary{Status: automations.RunStatus(outcome)}
	switch outcome {
	case "running":
		completed = nil
	case "failed", "interrupted":
		failure = "retained_failure"
	case "succeeded":
		mode, result, want.ConditionSummary = "evaluated", "true", automations.EvaluatedSummary{
			Result: automations.ConditionTrue,
		}
	case "bypassed":
		status, mode, bypass = "succeeded", "bypassed", 1
		want.Body, want.ConditionSummary = automations.RunHistorySummary{
			Status: automations.RunSucceeded,
		}, automations.BypassedSummary{}
	default:
		kind, skipSource, reason, triggers = "skip", source, outcome, `[{"id":"cron","kind":"cron","expression":"* * * * *"}]`
		if source == "manual" {
			triggers = `[]`
		}
		snapshot, runSource, status, started, completed, matched = nil, nil, nil, nil, nil, nil
		want.ID, want.Body = newSkipIDString(t), automations.SkipHistorySummary{Reason: automations.SkipReason(outcome)}
		mode, want.ConditionSummary = "not_evaluated", automations.NotEvaluatedSummary{}
		switch outcome {
		case "conditions_false":
			mode, result, want.ConditionSummary = "evaluated", "false", automations.EvaluatedSummary{
				Result: automations.ConditionFalse,
			}
		case "conditions_unknown":
			mode, result, want.ConditionSummary = "evaluated", "unknown", automations.EvaluatedSummary{
				Result: automations.ConditionUnknown,
			}
		}
	}
	decision := fmt.Sprintf(`{"mode":%q,"bypass_requested":%t}`, mode, bypass == 1)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, revision, recorded_at, kind,
		fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_previous_value_json, fact_emitted_at,
		hold_trigger_id, hold_started_at, hold_due_at,
		run_snapshot_json, run_source, run_status, run_failure_code, run_started_at, run_completed_at, run_matched_trigger_ids_json,
		skip_source, skip_reason, skip_matched_triggers_json, condition_mode, condition_result, condition_bypassed, condition_decision_json
	) VALUES (?, ?, 'Retained summary', 9, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		want.ID, parent, migrationTimestamp, kind, factID, family, entity, variant, causation, value, previous, emitted,
		holdID, start, due, snapshot, runSource, status, failure, started, completed, matched,
		skipSource, reason, triggers, mode, result, bypass, decision)
	return want
}

func TestHistoryProjectionRejectsContradictoryProvenanceOnBothPages(t *testing.T) {
	t.Parallel()
	for _, assignment := range []string{
		"run_status = 'unknown'", "revision = 0", "recorded_at = '0000'",
		"fact_previous_value_json = 'null'", "fact_family = 'observation'",
		"hold_trigger_id = 'held'", "run_source = 'device_fact'", "run_source = 'held_state'",
		"hold_trigger_id = 'held', hold_started_at = 'invalid', hold_due_at = 'invalid'",
		"hold_trigger_id = 'held', hold_started_at = recorded_at, hold_due_at = recorded_at",
		"run_source = 'schedule', condition_mode = 'bypassed', condition_bypassed = 1",
		"run_source = 'schedule', condition_mode = 'evaluated', condition_result = 'false'",
		"condition_mode = 'evaluated', condition_result = NULL",
		"condition_mode = 'not_configured', condition_result = 'true'",
		"condition_mode = 'bypassed', condition_bypassed = 0",
		"condition_mode = 'not_configured', condition_bypassed = 1",
	} {
		t.Run(assignment, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			parent := newAutomationIDString(t)
			id := insertRetainedHistoryRun(t, database, parent, "succeeded", admissionNow)
			// Simulate persisted corruption that bypassed normal write constraints.
			mustExec(t, database, "PRAGMA ignore_check_constraints = ON")
			mustExec(t, database, "DROP TRIGGER automation_schedule_history_update")
			mustExec(t, database, "UPDATE automation_history SET "+assignment+" WHERE id = ?", id)
			params := automations.ListHistoryParams{AutomationID: automations.AutomationID(parent), Limit: 10}
			for _, continuation := range []bool{false, true} {
				if continuation {
					at, cursor := admissionNow.Add(time.Hour), newRunIDString(t)
					params.BeforeRecordedAt, params.BeforeID = &at, &cursor
				}
				_, err := repository.ListHistory(t.Context(), params)
				var timestampError *time.ParseError
				if !errors.Is(err, automations.ErrInvalidAutomation) && !errors.As(err, &timestampError) {
					t.Fatalf("after=%t corruption error = %v", continuation, err)
				}
			}
		})
	}
}

func TestHistoryProjectionIgnoresMalformedDetailDocuments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ kind, assignment string }{
		{"run", "run_snapshot_json = '{}'"},
		{"run", "run_matched_trigger_ids_json = '[7]'"},
		{"skip", "skip_matched_triggers_json = '[7]'"},
		{"run", "condition_decision_json = '{}'"},
		{"skip", "condition_decision_json = '{}'"},
	} {
		t.Run(test.assignment+"/"+test.kind, func(t *testing.T) {
			t.Parallel()
			assertMalformedHistoryDetail(t, test.kind, test.assignment)
		})
	}
}

func assertMalformedHistoryDetail(t *testing.T, kind, assignment string) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	entity := definition.Triggers[0].Body.(automations.ObservationTrigger).EntityID
	var entryID string
	for range 2 {
		admitted, admitErr := repository.AdmitDeviceFact(
			t.Context(),
			observationFactFor(t, entity, admissionNow),
			devices.EntityStateSnapshot{},
			admissionNow,
			admissionNow.Add(-time.Minute),
		)
		if admitErr != nil {
			t.Fatal(admitErr)
		}
		if kind == "run" && len(admitted.StartedRuns) == 1 {
			entryID = string(admitted.StartedRuns[0].ID)
		} else if kind == "skip" && len(admitted.Skips) == 1 {
			entryID = string(admitted.Skips[0].SkipID)
		}
	}
	entry := historyEntry(t, repository, record.ID, entryID)
	assertFullHistoryBeforeCorruption(t, entry, definition.Name)
	want := listHistory(t, repository, record.ID)
	mustExec(t, database, "UPDATE automation_history SET "+assignment+" WHERE id = ?", entryID)
	params := automations.ListHistoryParams{AutomationID: record.ID, Limit: 10}
	for _, continuation := range []bool{false, true} {
		if continuation {
			at, cursor := admissionNow.Add(time.Hour), newRunIDString(t)
			params.BeforeRecordedAt, params.BeforeID = &at, &cursor
		}
		page, listErr := repository.ListHistory(t.Context(), params)
		if listErr != nil || page.HasMore || !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("after=%t summaries changed: %#v, %v; want %#v", continuation, page, listErr, want)
		}
	}
	_, err = repository.GetHistoryEntry(t.Context(), record.ID, entryID)
	var documentError *json.UnmarshalTypeError
	if !errors.Is(err, automations.ErrInvalidAutomation) && !errors.As(err, &documentError) {
		t.Fatalf("malformed detail error = %v", err)
	}
}

func assertFullHistoryBeforeCorruption(t *testing.T, entry automations.HistoryEntry, name string) {
	t.Helper()
	switch entry := entry.(type) {
	case automations.Run:
		if entry.Snapshot.Name != name || len(entry.Steps) != 1 || len(entry.MatchedTriggerIDs) != 1 {
			t.Fatalf("retained full Run = %#v", entry)
		}
	case automations.Skip:
		if entry.Reason != automations.SkipBusy || len(entry.MatchedTriggers) != 1 {
			t.Fatalf("retained full Skip = %#v", entry)
		}
	}
}
