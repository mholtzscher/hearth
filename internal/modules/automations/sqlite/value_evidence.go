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
const valueFailureReserve = 1024

// RecordRunValueInitialization seals initialization and any failure with the parent.
func (repo *AutomationRepository) RecordRunValueInitialization(
	ctx context.Context,
	id automations.RunID,
	init automations.RunValueInitialization,
) error {
	if _, err := automations.ParseRunID(string(id)); err != nil {
		return err
	}
	raw, err := automations.EncodeRunValueInitialization(init)
	if err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, parentErr := runningExecutionParent(ctx, queries, id)
		if parentErr != nil {
			return parentErr
		}
		if err = automations.ValidateRunValueInitializationWithPreparedSnapshot(init, parent.snapshot); err != nil {
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

// valueEnvelopeBytes counts the actual canonical public execution envelope. The
// evidence is already encoded by the transition helper, so only the small outer
// envelope is marshaled here; prior payloads are never marshaled on a transition.
func valueEnvelopeBytes(
	execution automations.StepExecution,
	envelope automations.StepExecutionEnvelope,
) (int64, error) {
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
	return int64(len(raw) - 2 + len(envelope.EvidenceJSON)), nil
}

// One record is charged for initialization and every reached value-bearing
// execution. A Function output/failure is a second record and charges the
// difference between its final and running public envelopes, not its inputs twice.
// One record and 1024 bytes remain available for a terminal value failure.
func accountValue(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	bytes int64,
	failure bool,
) error {
	limit, records := int64(valueEvidenceLimit-valueFailureReserve), int64(valueEvidenceRecords-1)
	if failure {
		limit, records = valueEvidenceLimit, valueEvidenceRecords
	}
	changed, err := queries.AddRunValueEvidence(ctx, dbsqlc.AddRunValueEvidenceParams{
		RunID: string(id), Bytes: bytes, Records: 1, ByteLimit: limit, RecordLimit: records,
	})
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
	return accountValue(ctx, queries, id, bytes, failure)
}

func reachedValueKind(details automations.StepExecutionDetails) (bool, bool) {
	switch d := details.(type) {
	case automations.CommandExecutionDetails:
		return len(d.Inputs) != 0, false
	case automations.NotificationExecutionDetails:
		return len(d.Inputs) != 0, false
	case automations.FunctionExecutionDetails:
		return len(d.Inputs) != 0, false
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
	if d, ok := details.(automations.FunctionExecutionDetails); ok {
		if d.ValueFailure == nil && len(d.Output) == 0 {
			return nil
		}
		before := automations.StepExecutionEnvelope{
			Kind:         automations.StepKindFunction,
			Phase:        "running",
			StartedAt:    d.StartedAt,
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
			return lifecycleCorruption("Function value envelope shrank")
		}
		return accountValue(ctx, queries, id, afterBytes-beforeBytes, d.ValueFailure != nil)
	}
	return nil
}

// validateValueAccounting reconstructs each originally charged reservation only
// on detail reads. Later non-value lifecycle completions do not alter its charge.
//
//nolint:gocognit // The accounting mirrors the distinct retained transition cases.
func validateValueAccounting(
	row dbsqlc.AutomationHistory,
	init *automations.RunValueInitialization,
	trace []automations.StepExecution,
) error {
	if row.DataEvidence != automations.RunDataRecorded {
		for _, execution := range trace {
			if value, _ := reachedValueKind(execution.Details); value {
				return lifecycleCorruption("legacy Run has value execution evidence")
			}
			if function, ok := execution.Details.(automations.FunctionExecutionDetails); ok &&
				(len(function.Output) != 0 || function.ValueFailure != nil) {
				return lifecycleCorruption("legacy Run has Function value evidence")
			}
		}
		return nil
	}
	var bytes, records int64
	if init != nil {
		raw, err := automations.EncodeRunValueInitialization(*init)
		if err != nil {
			return err
		}
		bytes, records = int64(len(raw)), 1
	}
	for _, execution := range trace {
		value, _ := reachedValueKind(execution.Details)
		if !value {
			continue
		}
		original, err := initialValueDetails(execution.Details)
		if err != nil {
			return err
		}
		initial, err := automations.EncodeStepExecutionDetails(original)
		if err != nil {
			return err
		}
		startBytes, err := valueEnvelopeBytes(execution, initial)
		if err != nil {
			return err
		}
		bytes += startBytes
		records++
		if function, ok := execution.Details.(automations.FunctionExecutionDetails); ok &&
			(len(function.Output) != 0 || function.ValueFailure != nil) {
			terminal, encodeErr := automations.EncodeStepExecutionDetails(function)
			if encodeErr != nil {
				return encodeErr
			}
			terminalBytes, sizeErr := valueEnvelopeBytes(execution, terminal)
			if sizeErr != nil {
				return sizeErr
			}
			if terminalBytes < startBytes {
				return lifecycleCorruption("Function value envelope shrank")
			}
			bytes += terminalBytes - startBytes
			records++
		}
	}
	if bytes != row.ValueEvidenceBytes || records != row.ValueEvidenceRecords {
		return lifecycleCorruption("Run value accounting disagrees with retained evidence")
	}
	return nil
}

func initialValueDetails(details automations.StepExecutionDetails) (automations.StepExecutionDetails, error) {
	switch d := details.(type) {
	case automations.CommandExecutionDetails:
		var reservation *automations.CommandReservation
		switch state := d.State.(type) {
		case automations.CommandRunning:
			reservation = &state.Reservation
		case automations.CommandSucceeded:
			reservation = &state.Reservation
		case automations.CommandFailed:
			reservation = &state.Reservation
		case automations.CommandInterrupted:
			reservation = state.Reservation
		}
		if reservation == nil {
			return nil, lifecycleCorruption("value inputs without Command reservation")
		}
		d.State = automations.CommandRunning{Reservation: *reservation}
		return d, nil
	case automations.NotificationExecutionDetails:
		d.State = automations.NotificationRunning{}
		return d, nil
	case automations.FunctionExecutionDetails:
		d.State = automations.FunctionRunning{}
		d.Output, d.ValueFailure = nil, nil
		return d, nil
	case automations.BranchExecutionDetails, automations.VariablesExecutionDetails,
		automations.StateExecutionDetails, automations.ValueFailureExecutionDetails:
		return details, nil
	case automations.DelayExecutionDetails:
		return nil, lifecycleCorruption("Delay has no value record")
	default:
		return nil, lifecycleCorruption("unknown value execution")
	}
}
