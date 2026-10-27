package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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

// Value accounting measures canonical single-field envelopes. The initialization
// uses its complete canonical document; each reached inputs, assignment, State,
// or failure and each Function completion output/failure is a separate record.
// This never re-encodes a prior payload during a transition.
func accountValue(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	field string,
	value any,
	failure bool,
) error {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(map[string]any{field: value})
	if err != nil {
		return err
	}
	limit, records := int64(valueEvidenceLimit-valueFailureReserve), int64(valueEvidenceRecords-1)
	if failure {
		limit, records = valueEvidenceLimit, valueEvidenceRecords
	}
	changed, err := queries.AddRunValueEvidence(ctx, dbsqlc.AddRunValueEvidenceParams{
		RunID: string(id), Bytes: int64(len(raw)), Records: 1, ByteLimit: limit, RecordLimit: records,
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
	details automations.StepExecutionDetails,
) error {
	switch d := details.(type) {
	case automations.CommandExecutionDetails:
		if len(d.Inputs) != 0 {
			return accountValue(ctx, queries, id, "inputs", d.Inputs, false)
		}
	case automations.NotificationExecutionDetails:
		if len(d.Inputs) != 0 {
			return accountValue(ctx, queries, id, "inputs", d.Inputs, false)
		}
	case automations.FunctionExecutionDetails:
		if len(d.Inputs) != 0 {
			return accountValue(ctx, queries, id, "inputs", d.Inputs, false)
		}
	case automations.VariablesExecutionDetails:
		return accountValue(ctx, queries, id, "values", d.Values, false)
	case automations.StateExecutionDetails:
		return accountValue(ctx, queries, id, "state", struct {
			Output         json.RawMessage                     `json:"output"`
			ReadAt         any                                 `json:"read_at"`
			Observation    *automations.ObservationEvidence    `json:"observation,omitempty"`
			UnusableReason *automations.ConditionUnknownReason `json:"unusable_reason,omitempty"`
		}{d.Output, d.ReadAt, d.Observation, d.UnusableReason}, false)
	case automations.ValueFailureExecutionDetails:
		return accountValue(ctx, queries, id, "value_failure", d.Failure, true)
	case automations.BranchExecutionDetails:
		if d.ValueFailure != nil {
			return accountValue(ctx, queries, id, "value_failure", d.ValueFailure, true)
		}
	case automations.DelayExecutionDetails:
	}
	return nil
}

func accountCompletionEvidence(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	details automations.StepExecutionDetails,
) error {
	if d, ok := details.(automations.FunctionExecutionDetails); ok {
		if d.ValueFailure != nil {
			return accountValue(ctx, queries, id, "value_failure", d.ValueFailure, true)
		}
		if len(d.Output) != 0 {
			return accountValue(ctx, queries, id, "output", d.Output, false)
		}
	}
	return nil
}
