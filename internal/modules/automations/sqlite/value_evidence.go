package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

const valueEvidenceLimit = 2 * 1024 * 1024
const valueEvidenceRecords = 256
const valueFailureReserve = 4096

// A running effect must leave room for production lifecycle fields (completion
// timestamp, verified identities, outcome, catalog revision and bounded reasons).
// This is an eligibility allowance, not retained evidence or an extra record;
// completion charges only its actual canonical envelope growth.
const valueCompletionAllowance = 512

// RecordRunValueInitialization seals initialization and any failure with the parent.
func (repo *AutomationRepository) RecordRunValueInitialization(
	ctx context.Context,
	id automations.RunID,
	init automations.RunValueInitialization,
) error {
	if _, err := automations.ParseRunID(string(id)); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, parentErr := runningExecutionParent(ctx, queries, id)
		if parentErr != nil {
			return parentErr
		}
		raw, err := automations.EncodeRunValueInitializationWithPreparedSnapshot(init, parent.snapshot)
		if err != nil {
			return err
		}
		limit := valueEvidenceLimit - valueFailureReserve
		if _, failed := init.Body.(automations.InitializationFailed); failed {
			limit = valueEvidenceLimit
		}
		if len(raw) > limit {
			return valueCapacityError()
		}
		changed, updateErr := queries.InitializeRunValues(ctx, dbsqlc.InitializeRunValuesParams{
			RunID: string(id), Initialization: sql.NullString{String: string(raw), Valid: true}, Bytes: int64(len(raw)),
		})
		if updateErr != nil {
			return updateErr
		}
		if changed != 1 {
			return lifecycleCorruption("Run initialization is not insertable")
		}
		if failure, ok := init.Body.(automations.InitializationFailed); ok {
			return completeExecutionRun(
				ctx,
				queries,
				id,
				automations.FailedRun{FailureCode: failure.Failure.FailureCode},
				init.RecordedAt,
			)
		}
		return nil
	})
}

// valueEnvelopeBytes counts the canonical value-bearing execution envelope, except
// pre-existing Condition evaluations, which retain their independent history
// accounting. Their complete diagnostics stay in the public timeline. Transitions
// use the current encoded evidence, never previous execution payloads.
func valueEnvelopeBytes(
	execution automations.StepExecution,
	envelope automations.StepExecutionEnvelope,
) (int64, error) {
	evidenceBytes := len(envelope.EvidenceJSON)
	if _, branch := execution.Details.(automations.BranchExecutionDetails); branch {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(envelope.EvidenceJSON, &fields); err != nil {
			return 0, err
		}
		delete(fields, "evaluations")
		valueFields, err := json.Marshal(fields)
		if err != nil {
			return 0, err
		}
		evidenceBytes = len(valueFields)
	}
	outer := struct {
		StepID      automations.StepID   `json:"step_id"`
		Sequence    int                  `json:"sequence"`
		Kind        automations.StepKind `json:"kind"`
		Phase       string               `json:"phase"`
		StartedAt   time.Time            `json:"started_at"`
		CompletedAt *time.Time           `json:"completed_at,omitempty"`
		Evidence    json.RawMessage      `json:"evidence"`
	}{execution.StepID, execution.Sequence, envelope.Kind, envelope.Phase, envelope.StartedAt, envelope.CompletedAt, json.RawMessage(`{}`)}
	raw, err := json.Marshal(outer)
	if err != nil {
		return 0, err
	}
	return int64(len(raw) - 2 + evidenceBytes), nil
}

func addValueEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	charge dbsqlc.AddRunValueEvidenceParams,
) error {
	changed, err := queries.AddRunValueEvidence(ctx, charge)
	if err != nil {
		return err
	}
	if changed != 1 {
		return valueCapacityError()
	}
	return nil
}

func valueCapacityError() error {
	return fmt.Errorf("automation value evidence capacity: %w", &exprvalue.Failure{Code: "run_data_limit"})
}

func accountReachedEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	execution automations.StepExecution,
	envelope automations.StepExecutionEnvelope,
) error {
	value, failure := reachedValueKind(execution.Details)
	if !value {
		return nil
	}
	bytes, err := valueEnvelopeBytes(execution, envelope)
	if err != nil {
		return err
	}
	limit, records := int64(valueEvidenceLimit-valueFailureReserve), int64(valueEvidenceRecords-1)
	if failure {
		limit, records = valueEvidenceLimit, valueEvidenceRecords
	} else if envelope.CompletedAt == nil {
		limit -= valueCompletionAllowance
	}
	return addValueEvidence(ctx, queries, dbsqlc.AddRunValueEvidenceParams{
		RunID: string(id), Bytes: bytes, Records: 1, ByteLimit: limit, RecordLimit: records,
	})
}

func reachedValueKind(details automations.StepExecutionDetails) (bool, bool) {
	switch d := details.(type) {
	case automations.CommandExecutionDetails:
		return d.Parameters != nil, false
	case automations.NotificationExecutionDetails:
		return d.Message != nil, false
	case automations.FunctionExecutionDetails:
		return d.Arguments != nil, false
	case automations.VariablesExecutionDetails:
		return true, false
	case automations.StateExecutionDetails:
		return true, false
	case automations.ValueFailureExecutionDetails:
		return true, true
	case automations.BranchExecutionDetails:
		return d.ValueFailure != nil, d.ValueFailure != nil
	case automations.DelayExecutionDetails:
	}
	return false, false
}

func accountCompletionEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	previous automations.StepExecution,
	previousEvidence json.RawMessage,
	details automations.StepExecutionDetails,
	envelope automations.StepExecutionEnvelope,
) error {
	if value, _ := reachedValueKind(previous.Details); !value {
		return nil
	}
	before := automations.StepExecutionEnvelope{
		Kind:         previous.Details.Kind(),
		Phase:        previous.Details.Phase(),
		StartedAt:    previous.Details.StartTime(),
		EvidenceJSON: previousEvidence,
	}
	beforeBytes, err := valueEnvelopeBytes(previous, before)
	if err != nil {
		return err
	}
	afterBytes, err := valueEnvelopeBytes(previous, envelope)
	if err != nil {
		return err
	}
	if afterBytes < beforeBytes {
		return lifecycleCorruption("value envelope shrank")
	}
	charge := dbsqlc.AddRunValueEvidenceParams{
		RunID: string(id), Bytes: afterBytes - beforeBytes,
		ByteLimit: valueEvidenceLimit - valueFailureReserve, RecordLimit: valueEvidenceRecords - 1,
	}
	if d, ok := details.(automations.FunctionExecutionDetails); ok && (d.ValueFailure != nil || len(d.Output) != 0) {
		charge.Records = 1
		if d.ValueFailure != nil {
			charge.ByteLimit, charge.RecordLimit = valueEvidenceLimit, valueEvidenceRecords
		}
	}
	return addValueEvidence(ctx, queries, charge)
}

func accountInterruptedEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	scope string,
	at time.Time,
	reason string,
) error {
	rows, err := queries.ListRunningValueExecutions(ctx, dbsqlc.ListRunningValueExecutionsParams{Scope: scope})
	if err != nil {
		return err
	}
	for _, row := range rows {
		execution, _, decodeErr := executionFromRow(row)
		if decodeErr != nil {
			return decodeErr
		}
		var completion automations.CompletedStepEvidence
		switch d := execution.Details.(type) {
		case automations.CommandExecutionDetails:
			state, ok := d.State.(automations.CommandRunning)
			if !ok {
				return lifecycleCorruption("interruption target is not running")
			}
			completion = automations.CommandFinished{State: automations.CommandInterrupted{
				CompletedAt: at, FailureCode: reason, Reservation: &state.Reservation,
			}}
		case automations.NotificationExecutionDetails:
			completion = automations.NotificationFinished{AttemptID: d.AttemptID,
				State: automations.NotificationInterrupted{CompletedAt: at, ReasonCode: reason}}
		case automations.FunctionExecutionDetails:
			completion = automations.FunctionFinished{InvocationID: d.InvocationID,
				State: automations.FunctionInterrupted{CompletedAt: at, ReasonCode: reason}}
		case automations.BranchExecutionDetails, automations.DelayExecutionDetails, automations.StateExecutionDetails,
			automations.ValueFailureExecutionDetails, automations.VariablesExecutionDetails:
			return lifecycleCorruption("unexpected value interruption kind")
		default:
			return lifecycleCorruption("unexpected value interruption kind")
		}
		details, envelope, completionErr := automations.CompleteStepDetails(
			execution.Details,
			automations.StepExecutionCompletion{
				RunID: automations.RunID(row.RunID), StepID: execution.StepID, Evidence: completion,
			},
		)
		if completionErr != nil {
			return completionErr
		}
		if accountingErr := accountCompletionEvidence(ctx, queries, automations.RunID(row.RunID), execution,
			json.RawMessage(row.EvidenceJson), details, envelope); accountingErr != nil {
			return accountingErr
		}
	}
	return nil
}
