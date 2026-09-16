package sqlite_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// skipProvenanceColumns is the shared column list of every provenance insert.
const skipProvenanceColumns = `id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json`

// insertSkipSQL inserts one Skip with an explicit provenance combination. It
// deliberately spells out every nullable column so a test can build both valid
// and contradictory rows without a second schema.
const insertSkipSQL = `INSERT INTO automation_history (` + skipProvenanceColumns + `)
    VALUES (?, ?, 'Office light', 'skip', 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// skipProvenanceArgs assembles one insert's bind arguments in column order.
type skipProvenanceArgs struct {
	id          string
	automation  string
	recordedAt  string
	factID      any
	family      any
	entityID    any
	variant     any
	causationID any
	valueJSON   any
	emittedAt   any
	triggers    any
	reason      any
	source      any
	decision    any
}

func (args skipProvenanceArgs) values() []any {
	return []any{
		args.id, args.automation, args.recordedAt,
		args.factID, args.family, args.entityID, args.variant, args.causationID,
		args.valueJSON, args.emittedAt,
		args.triggers, args.reason, args.source, args.decision,
	}
}

// Raw SQL must enforce Skip provenance and decision presence: a null-valued
// CHECK expression must never admit a Skip that lost its source or explanation.
func TestMigrationEnforcesSkipProvenanceAndDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	automationID := newAutomationIDString(t)
	factID := newFactIDString(t)
	factEntityID := string(newEntityID(t))
	factObservationID := newObservationIDString(t)
	triggers := matchedTriggerJSON(t)
	decision := `{"mode":"not_configured","bypass_requested":false}`
	// factBacked fills one complete observation Fact summary.
	factBacked := func() skipProvenanceArgs {
		return skipProvenanceArgs{
			id: newSkipIDString(t), automation: automationID, recordedAt: migrationTimestamp,
			factID: factID, family: "observation", entityID: factEntityID, variant: "applied",
			causationID: factObservationID, valueJSON: "true", emittedAt: migrationTimestamp,
		}
	}
	rejected := []struct {
		name string
		args skipProvenanceArgs
	}{
		{"new skip without a source", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason, args.decision = triggers, "automation_busy", decision
			return args
		}()},
		{"new skip without a decision", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason, args.source = triggers, "automation_busy", "device_fact"
			return args
		}()},
		{"device fact skip without triggers", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason, args.source, args.decision = "[]", "automation_busy", "device_fact", decision
			return args
		}()},
		{"device fact skip with partial Fact evidence", func() skipProvenanceArgs {
			args := factBacked()
			args.valueJSON = nil
			args.triggers, args.reason, args.source, args.decision = triggers, "automation_busy", "device_fact", decision
			return args
		}()},
		{"manual skip with a fact", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason, args.source, args.decision = "[]", "conditions_false", "manual", decision
			return args
		}()},
		{"manual skip with nonempty triggers", func() skipProvenanceArgs {
			args := factBacked()
			args.factID, args.family, args.entityID = nil, nil, nil
			args.variant, args.causationID, args.valueJSON, args.emittedAt = nil, nil, nil, nil
			args.triggers, args.reason, args.source, args.decision = triggers, "conditions_false", "manual", decision
			return args
		}()},
		{"manual skip with an automatic reason", func() skipProvenanceArgs {
			args := factBacked()
			args.factID, args.family, args.entityID = nil, nil, nil
			args.variant, args.causationID, args.valueJSON, args.emittedAt = nil, nil, nil, nil
			args.triggers, args.reason, args.source, args.decision = "[]", "stale_fact", "manual", decision
			return args
		}()},
		{"condition reason without a decision", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason = triggers, "conditions_unknown"
			return args
		}()},
		{"decision is not an object", func() skipProvenanceArgs {
			args := factBacked()
			args.triggers, args.reason, args.source, args.decision = triggers, "automation_busy", "device_fact", "[]"
			return args
		}()},
	}
	for _, test := range rejected {
		if _, err := database.ExecContext(ctx, insertSkipSQL, test.args.values()...); err == nil {
			t.Fatalf("%s: invalid provenance was accepted", test.name)
		}
	}

	// A run row may not carry Skip provenance, and a legacy skip must carry
	// complete Fact evidence with an unconditional automatic reason.
	if _, err := database.ExecContext(ctx, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, skip_source)
		VALUES (?, ?, 'Office light', 'run', 1, ?, '{}', 'manual', 'running', ?, '[]', 'manual')`,
		newRunIDString(t), automationID, migrationTimestamp, migrationTimestamp,
	); err == nil {
		t.Fatal("a run row with skip provenance was accepted")
	}
	legacyConditionReason := func() skipProvenanceArgs {
		args := factBacked()
		args.triggers, args.reason = triggers, "conditions_false"
		return args
	}()
	if _, err := database.ExecContext(ctx, insertSkipSQL, legacyConditionReason.values()...); err == nil {
		t.Fatal("a legacy skip with a Condition reason was accepted")
	}

	// A device-fact Skip, a manual Skip, and a legacy Skip all commit.
	deviceFact := factBacked()
	deviceFact.triggers, deviceFact.reason = triggers, "automation_busy"
	deviceFact.source, deviceFact.decision = "device_fact", decision
	if _, err := database.ExecContext(ctx, insertSkipSQL, deviceFact.values()...); err != nil {
		t.Fatalf("valid device-fact Skip: %v", err)
	}
	manual := skipProvenanceArgs{
		id: newSkipIDString(t), automation: automationID, recordedAt: migrationTimestamp,
		triggers: "[]", reason: "conditions_false", source: "manual", decision: decision,
	}
	if _, err := database.ExecContext(ctx, insertSkipSQL, manual.values()...); err != nil {
		t.Fatalf("valid manual Skip: %v", err)
	}
	if _, err := database.ExecContext(ctx, legacyInsertHistorySkipSQL,
		newSkipIDString(t), automationID, migrationTimestamp, newFactIDString(t), factEntityID, factObservationID,
		"true", migrationTimestamp, triggers,
	); err != nil {
		t.Fatalf("valid legacy Skip: %v", err)
	}
}

// Legacy unconditioned history and a manual Condition Skip must both decode:
// the legacy row normalizes to device_fact/not_configured, and the manual row
// keeps a null Fact with an evaluated false decision.
func TestHistoryDecodesLegacyAndManualSkipProvenance(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	legacyAutomation := automations.AutomationID(newAutomationIDString(t))
	manualAutomation := automations.AutomationID(newAutomationIDString(t))
	factID := newFactIDString(t)
	factEntityID := string(newEntityID(t))
	factObservationID := newObservationIDString(t)
	triggers := matchedTriggerJSON(t)

	legacyID := newSkipIDString(t)
	mustExec(t, database, legacyInsertHistorySkipSQL,
		legacyID, string(legacyAutomation), migrationTimestamp, factID, factEntityID, factObservationID,
		"true", migrationTimestamp, triggers,
	)
	legacy := historyEntry(t, repository, legacyAutomation, legacyID)
	if legacy.Skip == nil {
		t.Fatalf("legacy history entry = %#v, want a Skip", legacy)
	}
	if legacy.Skip.Source != automations.RunSourceDeviceFact || legacy.Skip.Fact == nil ||
		legacy.Skip.ConditionDecision.Mode != automations.AutomationConditionDecisionNotConfigured ||
		len(legacy.Skip.MatchedTriggers) != 1 {
		t.Fatalf("legacy Skip = %#v", legacy.Skip)
	}
	legacySummary := firstHistorySummary(t, repository, legacyAutomation)
	if legacySummary.Source != automations.RunSourceDeviceFact ||
		legacySummary.ConditionMode != automations.AutomationConditionDecisionNotConfigured ||
		legacySummary.ConditionResult != nil {
		t.Fatalf("legacy summary = %#v", legacySummary)
	}

	// A manual Skip's decision must be the real evaluated false tree, so build it
	// through the evaluator rather than fabricating JSON.
	entityID := newEntityID(t)
	conditions := conditionLeaf("dark", entityID, automations.ComparisonLessThan, "30")
	evaluation, err := automations.EvaluateAutomationConditions(
		*conditions,
		stateSnapshotWith(presentStateEntry(t, entityID, `{"level":90}`, admissionNow)),
		admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := automations.EncodeAutomationConditionDecision(automations.AutomationConditionDecision{
		Mode:       automations.AutomationConditionDecisionEvaluated,
		Snapshot:   conditions,
		Evaluation: &evaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	manualID := newSkipIDString(t)
	mustExec(t, database, insertSkipSQL, skipProvenanceArgs{
		id: manualID, automation: string(manualAutomation), recordedAt: migrationTimestamp,
		triggers: "[]", reason: "conditions_false", source: "manual", decision: string(decision),
	}.values()...)

	manual := historyEntry(t, repository, manualAutomation, manualID)
	if manual.Skip == nil {
		t.Fatalf("manual history entry = %#v, want a Skip", manual)
	}
	if manual.Skip.Source != automations.RunSourceManual || manual.Skip.Fact != nil ||
		len(manual.Skip.MatchedTriggers) != 0 ||
		manual.Skip.ConditionDecision.Mode != automations.AutomationConditionDecisionEvaluated ||
		manual.Skip.ConditionDecision.Evaluation.Result != automations.AutomationConditionFalse {
		t.Fatalf("manual Skip = %#v", manual.Skip)
	}
	selected := manual.Skip.ConditionDecision.Evaluation.Nodes[0].SelectedValue
	if string(selected) != "90" {
		t.Fatalf("manual Skip selected value = %s", selected)
	}
	manualSummary := firstHistorySummary(t, repository, manualAutomation)
	if manualSummary.Source != automations.RunSourceManual || manualSummary.Fact != nil ||
		manualSummary.ConditionResult == nil || *manualSummary.ConditionResult != automations.AutomationConditionFalse {
		t.Fatalf("manual summary = %#v", manualSummary)
	}
	if !slices.ContainsFunc(listHistory(t, repository, manualAutomation),
		func(summary automations.AutomationHistorySummary) bool {
			return summary.ID == manualID && summary.ConditionMode ==
				automations.AutomationConditionDecisionEvaluated
		}) {
		t.Fatal("manual summary is missing from the history page")
	}
}

// A selected JSON null must stay distinct from a missing selection through one
// encoded and decoded decision, and the decoded leaf keeps its Observation time
// and identity rather than consulting newer State.
func TestConditionDecisionPreservesSelectedJSONNull(t *testing.T) {
	t.Parallel()
	entityID := newEntityID(t)
	conditions := conditionLeaf("flag", entityID, automations.ComparisonEqual, `null`)
	observedAt := admissionNow.Add(-time.Minute)
	evaluation, err := automations.EvaluateAutomationConditions(
		*conditions,
		stateSnapshotWith(presentStateEntry(t, entityID, `{"level":null}`, observedAt)),
		admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.Nodes) != 1 || string(evaluation.Nodes[0].SelectedValue) != "null" {
		t.Fatalf("selected-value evidence = %#v", evaluation.Nodes)
	}
	raw, err := automations.EncodeAutomationConditionDecision(automations.AutomationConditionDecision{
		Mode:       automations.AutomationConditionDecisionEvaluated,
		Snapshot:   conditions,
		Evaluation: &evaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeAutomationConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	leaf := decoded.Evaluation.Nodes[0]
	if string(leaf.SelectedValue) != "null" {
		t.Fatalf("decoded selected value = %q, want a selected JSON null", leaf.SelectedValue)
	}
	if leaf.ObservationID == nil || leaf.ObservedAt == nil || !leaf.ObservedAt.Equal(observedAt) {
		t.Fatalf("decoded leaf evidence = %#v", leaf)
	}
	if decoded.Evaluation.Result != automations.AutomationConditionTrue {
		t.Fatalf("decoded root result = %q", decoded.Evaluation.Result)
	}
}

// insertSummaryRunSQL inserts one Run with the full column set, so a test can
// control the retained definition snapshot independently of the SQL constraints.
const insertSummaryRunSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    run_snapshot_json, run_source, run_status, run_started_at, run_completed_at,
    run_matched_trigger_ids_json, condition_decision_json
) VALUES (?, ?, 'Office light', 'run', 1, ?, ?, 'observation', ?, 'applied', ?, 'true', ?,
    ?, ?, 'succeeded', ?, ?, '[]', ?)`

// A history summary needs only the decision envelope, so it must not decode the
// retained definition snapshot. A malformed run_snapshot_json that still
// satisfies the SQL CHECK is served as a summary, while the full history detail
// fails to decode it.
func TestHistorySummaryDoesNotDecodeRunSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)

	automationID := newAutomationIDString(t)
	runID := newRunIDString(t)
	// This JSON object is well within the size limit, so it satisfies the storage
	// CHECK, but it is not a decodable Automation definition.
	mustExec(t, database, insertSummaryRunSQL,
		runID, automationID, migrationTimestamp,
		newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), migrationTimestamp,
		`{"name":"only a name"}`, "device_fact", migrationTimestamp, migrationTimestamp,
		`{"mode":"not_configured","bypass_requested":false}`,
	)

	summaries := listHistory(t, repository, automations.AutomationID(automationID))
	if len(summaries) != 1 || summaries[0].ID != runID {
		t.Fatalf("history summaries = %#v, want the retained Run summary", summaries)
	}
	if summaries[0].Status != automations.RunSucceeded ||
		summaries[0].ConditionMode != automations.AutomationConditionDecisionNotConfigured {
		t.Fatalf("Run summary = %#v", summaries[0])
	}
	if _, err := repository.GetHistoryEntry(
		ctx, automations.AutomationID(automationID), runID,
	); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("malformed Run snapshot detail error = %v, want ErrInvalidAutomation", err)
	}
}
