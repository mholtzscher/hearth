//nolint:goconst,mnd // History evidence mirrors the persisted Run/Step vocabulary and fixture dimensions.
package dbload

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const fixtureRunFactBase = fixtureRunFactSequence

// insertAutomations seeds definitions, heads, retained Run/Skip history, valid
// Step evidence, fact receipts, held-state rows, and the schedule watermark.
//
//nolint:gocognit // One transaction prepares and seeds the complete Automation graph.
func insertAutomations(ctx context.Context, database *sql.DB, plan fixturePlan) (string, error) {
	firstAutomation := ""
	err := withFixtureTx(ctx, database, func(tx *sql.Tx) error {
		revisionInsert, err := prepareStatement(ctx, tx, `
INSERT INTO automation_definition_revisions (automation_id, revision, definition_json) VALUES (?, 1, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = revisionInsert.Close() }()
		headInsert, err := prepareStatement(ctx, tx, `
INSERT INTO automations (id, revision, created_at, updated_at) VALUES (?, 1, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = headInsert.Close() }()
		historyInsert, err := prepareStatement(ctx, tx, historyInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = historyInsert.Close() }()
		skipInsert, err := prepareStatement(ctx, tx, skipInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = skipInsert.Close() }()
		stepInsert, err := prepareStatement(ctx, tx, stepInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = stepInsert.Close() }()
		receiptInsert, err := prepareStatement(ctx, tx, `
INSERT INTO automation_fact_receipts (fact_id, automation_id)
VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = receiptInsert.Close() }()
		holdInsert, err := prepareStatement(ctx, tx, `
INSERT INTO automation_holds (
  automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at
) VALUES (?, 1, 'state', ?, 'consumed', NULL, NULL)`)
		if err != nil {
			return err
		}
		defer func() { _ = holdInsert.Close() }()
		factInsert, err := prepareStatement(ctx, tx, factInsertSQL)
		if err != nil {
			return err
		}
		defer func() { _ = factInsert.Close() }()
		for automationIndex := range plan.automations {
			automationID := fixtureID("aut_", fixtureIndex(automationIndex))
			if firstAutomation == "" {
				firstAutomation = automationID
			}
			if err = insertAutomation(
				ctx, plan, automationIndex, automationID,
				revisionInsert, headInsert, historyInsert, skipInsert, stepInsert,
				receiptInsert, holdInsert, factInsert,
			); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO automation_schedule_watermarks (id, highwater_at) VALUES ('global', ?)`,
			fixtureStamp(-time.Hour),
		)
		if err != nil {
			return fmt.Errorf("insert schedule watermark: %w", err)
		}
		return nil
	})
	return firstAutomation, err
}

const historyInsertSQL = `
INSERT INTO automation_history (
  id, automation_id, automation_name, kind, revision, recorded_at,
  fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
  fact_value_json, fact_emitted_at, definition_revision, run_source, run_status,
  run_failure_code, run_started_at, run_completed_at, run_matched_trigger_ids_json,
  skip_matched_triggers_json, skip_reason, skip_source, hold_trigger_id,
  hold_started_at, hold_due_at, condition_decision_json, condition_mode,
  condition_bypassed, condition_result, data_evidence
) VALUES (
  ?, ?, ?, 'run', 1, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL,
  NULL, NULL, NULL, ?, 'not_configured', 0, NULL, 'legacy_unavailable'
)`

const skipInsertSQL = `
INSERT INTO automation_history (
  id, automation_id, automation_name, kind, revision, recorded_at,
  skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json,
  condition_mode, condition_bypassed, data_evidence
) VALUES (?, ?, ?, 'skip', 1, ?, '[]', 'conditions_false', 'manual', ?, 'not_configured', 0, 'legacy_unavailable')`

const stepInsertSQL = `
INSERT INTO automation_step_executions (
  run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

const factInsertSQL = `
INSERT INTO device_facts_outbox (
  fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
  traceparent, tracestate, value_json, adapter_received_at, source_updated_at,
  observed_at, reported_at, received_at, recorded_at, previous_value_json
) VALUES (?, 'observation', ?, 'applied', ?, ?, ?, '', '', ?, ?, NULL, ?, NULL, NULL, NULL, NULL)`

type automationStatements struct {
	revision *sql.Stmt
	head     *sql.Stmt
	history  *sql.Stmt
	skip     *sql.Stmt
	step     *sql.Stmt
	receipt  *sql.Stmt
	hold     *sql.Stmt
	fact     *sql.Stmt
}

func insertAutomation(
	ctx context.Context,
	plan fixturePlan,
	automationIndex int,
	automationID string,
	revisionInsert, headInsert, historyInsert, skipInsert, stepInsert,
	receiptInsert, holdInsert, factInsert *sql.Stmt,
) error {
	statements := automationStatements{
		revision: revisionInsert, head: headInsert, history: historyInsert, skip: skipInsert,
		step: stepInsert, receipt: receiptInsert, hold: holdInsert, fact: factInsert,
	}
	deviceIndex := automationIndex % plan.devices
	powerEntityID := entityIDFor(deviceIndex, rolePower)
	definition, err := fixtureAutomationDefinition(automationIndex, powerEntityID)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("Fixture Automation %d", automationIndex)
	if _, err = statements.revision.ExecContext(ctx, automationID, string(definition)); err != nil {
		return fmt.Errorf("insert automation definition: %w", err)
	}
	if _, err = statements.head.ExecContext(
		ctx, automationID, fixtureStamp(-24*time.Hour), fixtureStamp(-time.Hour),
	); err != nil {
		return fmt.Errorf("insert automation head: %w", err)
	}
	if err = insertAutomationRuns(ctx, plan, automationIndex, automationID, name, deviceIndex, statements); err != nil {
		return err
	}
	if _, err = statements.skip.ExecContext(
		ctx, fixtureID("ask_", fixtureIndex(automationIndex)), automationID, name,
		fixtureStamp(-time.Duration(plan.runsPerAutomation+1)*time.Hour),
		fixtureConditionDecisionJSON,
	); err != nil {
		return fmt.Errorf("insert automation skip: %w", err)
	}
	anchorOrder := stateOrdinalFor(deviceIndex, rolePower)*plan.stateHistory + plan.stateHistory
	if _, err = statements.hold.ExecContext(ctx, automationID, anchorOrder); err != nil {
		return fmt.Errorf("insert automation hold: %w", err)
	}
	return nil
}

func insertAutomationRuns(
	ctx context.Context,
	plan fixturePlan,
	automationIndex int,
	automationID, name string,
	deviceIndex int,
	statements automationStatements,
) error {
	for run := range plan.runsPerAutomation {
		caseName := automationRunCase(run, plan.runsPerAutomation)
		runID := fixtureID("arn_", fixtureIndex(automationIndex*plan.runsPerAutomation+run))
		factID := ""
		if caseName == "device_fact" {
			factID = fixtureID("fct_", fixtureRunFactBase+fixtureIndex(automationIndex*plan.runsPerAutomation+run))
		}
		if err := insertRunRow(
			ctx, plan, automationIndex, run, automationID, name, runID, factID, caseName, statements.history,
		); err != nil {
			return err
		}
		if err := insertRunSteps(ctx, plan, deviceIndex, runID, caseName, run, statements.step); err != nil {
			return err
		}
		if caseName == "device_fact" {
			if err := insertRunFact(ctx, plan, deviceIndex, factID, statements.fact); err != nil {
				return err
			}
			if _, err := statements.receipt.ExecContext(ctx, factID, automationID); err != nil {
				return fmt.Errorf("insert automation fact receipt: %w", err)
			}
		}
	}
	return nil
}

func automationRunCase(run, total int) string {
	switch run {
	case 0:
		return "device_fact"
	case 1:
		return "schedule"
	case total - 3:
		return "interrupted"
	case total - 2:
		return "failed"
	case total - 1:
		return "running"
	default:
		return "manual"
	}
}

func insertRunRow(
	ctx context.Context,
	plan fixturePlan,
	automationIndex, run int,
	automationID, name, runID, factID, caseName string,
	insert *sql.Stmt,
) error {
	recorded := fixtureStamp(-time.Duration(plan.runsPerAutomation-run) * time.Hour)
	started := fixtureStamp(-time.Duration(plan.runsPerAutomation-run)*time.Hour - time.Minute)
	completed := fixtureStamp(-time.Duration(plan.runsPerAutomation-run)*time.Hour + time.Second)
	source, status, failure, matched := runSourceStatus(caseName)
	var factEntity, factVariant, factCausation, factValue, factEmitted any
	var completedAt any
	if caseName != "running" {
		completedAt = completed
	}
	if caseName == "device_fact" {
		deviceIndex := automationIndex % plan.devices
		anchor := plan.stateHistory - 1
		factEntity = entityIDFor(deviceIndex, rolePower)
		factVariant = "applied"
		factCausation = observationIDFor(deviceIndex, rolePower, anchor, plan.stateHistory)
		factValue = string(fixtureStateValue(rolePower, anchor))
		factEmitted = fixtureStamp(-time.Duration(plan.runsPerAutomation-run)*time.Hour - 2*time.Minute)
	}
	if _, err := insert.ExecContext(
		ctx, runID, automationID, name, recorded, nilIfEmpty(factID), factFamily(factID),
		factEntity, factVariant, factCausation, factValue, factEmitted, source, status, failure,
		started, completedAt, matched, fixtureConditionDecisionJSON,
	); err != nil {
		return fmt.Errorf("insert automation run: %w", err)
	}
	return nil
}

func runSourceStatus(caseName string) (string, string, any, string) {
	switch caseName {
	case "device_fact":
		return "device_fact", "succeeded", nil, "[]"
	case "schedule":
		return "schedule", "succeeded", nil, `["clock"]`
	case "interrupted":
		return "manual", "interrupted", "core_restarted", "[]"
	case "failed":
		return "manual", "failed", "command_failed", "[]"
	case "running":
		return "manual", "running", nil, "[]"
	default:
		return "manual", "succeeded", nil, "[]"
	}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func factFamily(factID string) any {
	if factID == "" {
		return nil
	}
	return "observation"
}

func commandEvidence(deviceIndex int, plan fixturePlan) string {
	commandIndex := deviceIndex*plan.commands + 2
	commandID := fixtureID("cmd_", fixtureIndex(commandIndex))
	correlationID := fixtureID("cor_", fixtureIndex(commandIndex))
	return fmt.Sprintf(
		`{"command_id":%q,"command_outcome":"satisfied","correlation_id":%q,"verified_command_id":%q}`,
		commandID, correlationID, commandID,
	)
}

func commandReservationEvidence(deviceIndex int, plan fixturePlan) string {
	commandIndex := deviceIndex*plan.commands + 2
	return fmt.Sprintf(
		`{"command_id":%q,"correlation_id":%q}`,
		fixtureID("cmd_", fixtureIndex(commandIndex)),
		fixtureID("cor_", fixtureIndex(commandIndex)),
	)
}

func insertRunSteps(
	ctx context.Context,
	plan fixturePlan,
	deviceIndex int,
	runID, caseName string,
	run int,
	insert *sql.Stmt,
) error {
	started := fixtureStamp(-time.Duration(plan.runsPerAutomation-run)*time.Hour - time.Minute)
	completed := fixtureStamp(-time.Duration(plan.runsPerAutomation-run) * time.Hour)
	evidence := commandEvidence(deviceIndex, plan)
	switch caseName {
	case "failed":
		commandIndex := deviceIndex*plan.commands + 2
		failedEvidence := fmt.Sprintf(
			`{"command_id":%q,"correlation_id":%q,"reason_code":"command_failed"}`,
			fixtureID("cmd_", fixtureIndex(commandIndex)),
			fixtureID("cor_", fixtureIndex(commandIndex)),
		)
		return execStep(ctx, insert, runID, "command", 0, "command", "failed", started, completed, failedEvidence)
	case "interrupted":
		return execStep(ctx, insert, runID, "command", 0, "command", "interrupted", started, completed,
			`{"reason_code":"core_restarted"}`)
	case "running":
		return execStep(ctx, insert, runID, "command", 0, "command", "running", started, nil,
			commandReservationEvidence(deviceIndex, plan))
	default:
		if err := execStep(
			ctx,
			insert,
			runID,
			"command",
			0,
			"command",
			"succeeded",
			started,
			completed,
			evidence,
		); err != nil {
			return err
		}
		return execStep(ctx, insert, runID, "wait", 1, "delay", "succeeded", completed, completed, `{}`)
	}
}

func execStep(
	ctx context.Context,
	insert *sql.Stmt,
	runID, stepID string,
	sequence int,
	kind, phase, started string,
	completed any,
	evidence string,
) error {
	if _, err := insert.ExecContext(
		ctx, runID, stepID, sequence, kind, phase, started, completed, evidence,
	); err != nil {
		return fmt.Errorf("insert automation step: %w", err)
	}
	return nil
}

func insertRunFact(
	ctx context.Context,
	plan fixturePlan,
	deviceIndex int,
	factID string,
	insert *sql.Stmt,
) error {
	anchor := plan.stateHistory - 1
	offset := -time.Duration(plan.runsPerAutomation) * time.Hour
	if _, err := insert.ExecContext(
		ctx, factID, entityIDFor(deviceIndex, rolePower),
		observationIDFor(deviceIndex, rolePower, anchor, plan.stateHistory),
		fixtureID("cor_", fixtureRunFactBase+fixtureIndex(deviceIndex)), fixtureStamp(offset),
		string(fixtureStateValue(rolePower, anchor)), fixtureStamp(offset), fixtureStamp(offset),
	); err != nil {
		return fmt.Errorf("insert automation device fact: %w", err)
	}
	return nil
}
