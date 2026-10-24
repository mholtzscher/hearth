package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

const (
	functionStatusRunning     = "running"
	functionStatusSucceeded   = "succeeded"
	functionStatusFailed      = "failed"
	functionStatusInterrupted = "interrupted"
)

// RecordFunctionStart reserves an invocation link before any external effect.
func (repo *AutomationRepository) RecordFunctionStart(ctx context.Context, start automations.FunctionStart) error {
	if err := automations.ValidateFunctionStart(start); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		run, err := startableEvidenceParent(ctx, queries, start.RunID)
		if err != nil {
			return err
		}
		if _, err = automations.FunctionWithPreparedSnapshot(run.Snapshot, start.StepID); err != nil {
			return err
		}
		if start.Position != len(run.Functions) {
			return functionCorruption("position is not appendable")
		}
		for _, previous := range run.Functions {
			if previous.StepID == start.StepID ||
				automations.FunctionStateStatus(previous.State) != functionStatusSucceeded {
				return functionCorruption("prior execution is not a success or Step already reached")
			}
		}
		return queries.CreateRunFunction(ctx, dbsqlc.CreateRunFunctionParams{
			RunID: string(start.RunID), StepID: string(start.StepID), Position: int64(start.Position),
			InvocationID: string(start.InvocationID), StartedAt: encodeAutomationTimestamp(start.StartedAt),
		})
	})
}

// CompleteFunction commits terminal evidence and a failed/interrupted parent atomically.
func (repo *AutomationRepository) CompleteFunction(
	ctx context.Context,
	completion automations.FunctionCompletion,
) error {
	if err := automations.ValidateFunctionCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		run, err := runningEvidenceParent(ctx, queries, completion.RunID)
		if err != nil {
			return err
		}
		if err = verifyFunctionReservation(run, completion); err != nil {
			return err
		}
		columns := functionStateColumns(completion.State)
		updated, err := queries.CompleteRunFunction(ctx, dbsqlc.CompleteRunFunctionParams{
			RunID: string(
				completion.RunID,
			),
			StepID:            string(completion.StepID),
			InvocationID:      string(completion.InvocationID),
			Status:            columns.status,
			CompletedAt:       columns.completedAt,
			CatalogRevision:   columns.revision,
			InvocationOutcome: columns.outcome,
			ReasonCode:        columns.reason,
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return functionCorruption("reservation is not completable")
		}
		if columns.status == functionStatusSucceeded {
			return nil
		}
		status := automations.RunFailed
		if columns.status == functionStatusInterrupted {
			status = automations.RunInterrupted
		}
		updated, err = queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
			ID: string(completion.RunID), RunStatus: sql.NullString{String: string(status), Valid: true},
			RunCompletedAt: columns.completedAt, RunFailureCode: columns.reason,
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return functionCorruption("parent is not completable")
		}
		return nil
	})
}

func verifyFunctionReservation(run automations.Run, completion automations.FunctionCompletion) error {
	for _, execution := range run.Functions {
		if execution.StepID == completion.StepID && execution.InvocationID == completion.InvocationID &&
			automations.FunctionStateStatus(execution.State) == functionStatusRunning {
			return nil
		}
	}
	return functionCorruption("completion does not match a running reservation")
}

type functionColumns struct {
	status      string
	completedAt sql.NullString
	revision    sql.NullString
	outcome     sql.NullString
	reason      sql.NullString
}

// functionStateColumns consumes a validated terminal state.
func functionStateColumns(state automations.FunctionExecutionState) functionColumns {
	columns := functionColumns{
		status: automations.FunctionStateStatus(state),
		completedAt: sql.NullString{
			String: encodeAutomationTimestamp(*automations.FunctionTerminalTime(state)),
			Valid:  true,
		},
	}
	switch state := state.(type) {
	case automations.FunctionSucceeded:
		columns.revision = sql.NullString{String: string(state.CatalogRevision), Valid: true}
		columns.outcome = sql.NullString{String: functionStatusSucceeded, Valid: true}
	case automations.FunctionFailed:
		switch evidence := state.Evidence.(type) {
		case automations.FunctionAdmissionRejected:
			columns.reason = sql.NullString{String: evidence.ReasonCode, Valid: true}
		case automations.FunctionInvocationFailed:
			columns.revision = sql.NullString{String: string(evidence.CatalogRevision), Valid: true}
			var outcome, reason string
			switch evidence := evidence.Outcome.(type) {
			case adapters.Failed:
				outcome, reason = functionStatusFailed, evidence.ReasonCode
			case adapters.NotExecuted:
				outcome, reason = "not_executed", evidence.ReasonCode
			case adapters.Unknown:
				outcome, reason = "unknown", evidence.ReasonCode
			case adapters.Succeeded:
				panic("validated failure contains success")
			}
			columns.outcome = sql.NullString{String: outcome, Valid: true}
			columns.reason = sql.NullString{String: reason, Valid: true}
		}
	case automations.FunctionInterrupted:
		columns.reason = sql.NullString{String: state.ReasonCode, Valid: true}
	case automations.FunctionRunning:
		panic("validated completion is running")
	}
	return columns
}

func runFunctions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.Run,
) ([]automations.FunctionExecution, error) {
	rows, err := queries.ListRunFunctions(ctx, dbsqlc.ListRunFunctionsParams{RunID: string(run.ID)})
	if err != nil {
		return nil, err
	}
	result := make([]automations.FunctionExecution, 0, len(rows))
	for position, row := range rows {
		execution, decodeErr := functionFromRow(row, run)
		if decodeErr != nil {
			return nil, decodeErr
		}
		status := automations.FunctionStateStatus(execution.State)
		if execution.Position != position || (position < len(rows)-1 && status != functionStatusSucceeded) {
			return nil, functionCorruption("order disagrees")
		}
		if automations.RunStateStatus(run.State) == automations.RunSucceeded && status != functionStatusSucceeded {
			return nil, functionCorruption("successful parent contains unsuccessful evidence")
		}
		result = append(result, execution)
	}
	return result, nil
}

func functionFromRow(row dbsqlc.AutomationRunFunction, run automations.Run) (automations.FunctionExecution, error) {
	start := automations.FunctionStart{
		RunID: run.ID, StepID: automations.StepID(row.StepID), Position: int(row.Position),
		InvocationID: adapters.InvocationID(row.InvocationID),
	}
	execution := automations.FunctionExecution{FunctionStart: start}
	startedAt, err := decodeAutomationTimestamp(row.StartedAt)
	if err != nil {
		return execution, err
	}
	execution.StartedAt = startedAt
	if err = automations.ValidateFunctionStart(execution.FunctionStart); err != nil {
		return execution, err
	}
	if _, err = automations.FunctionWithPreparedSnapshot(run.Snapshot, execution.StepID); err != nil {
		return execution, err
	}
	if row.RunID != string(run.ID) {
		return execution, functionCorruption("row ownership disagrees")
	}
	state, err := functionStateFromRow(row)
	if err != nil {
		return execution, err
	}
	execution.State = state
	if row.Status != functionStatusRunning {
		if err = automations.ValidateFunctionCompletion(automations.FunctionCompletion{
			RunID: run.ID, StepID: execution.StepID, InvocationID: execution.InvocationID, State: state,
		}); err != nil {
			return execution, err
		}
		if err = validateFunctionParentState(run, state); err != nil {
			return execution, err
		}
	}
	return execution, nil
}

func validateFunctionParentState(run automations.Run, state automations.FunctionExecutionState) error {
	parentAt, parentReason := automations.RunStateCompletion(run.State)
	childAt := automations.FunctionTerminalTime(state)
	status := automations.FunctionStateStatus(state)
	if status == functionStatusSucceeded {
		return nil
	}
	columns := functionStateColumns(state)
	wantStatus := automations.RunFailed
	if status == functionStatusInterrupted {
		wantStatus = automations.RunInterrupted
	}
	if automations.RunStateStatus(run.State) != wantStatus || parentAt == nil || !parentAt.Equal(*childAt) ||
		parentReason == nil || *parentReason != columns.reason.String {
		return functionCorruption("terminal failure and parent disagree")
	}
	return nil
}

func functionStateFromRow(row dbsqlc.AutomationRunFunction) (automations.FunctionExecutionState, error) {
	if row.Status == functionStatusRunning {
		if row.CompletedAt.Valid || row.CatalogRevision.Valid || row.InvocationOutcome.Valid || row.ReasonCode.Valid {
			return nil, functionCorruption("running evidence has terminal fields")
		}
		return automations.FunctionRunning{}, nil
	}
	if !row.CompletedAt.Valid {
		return nil, functionCorruption("terminal evidence lacks completion time")
	}
	at, err := decodeAutomationTimestamp(row.CompletedAt.String)
	if err != nil {
		return nil, err
	}
	switch row.Status {
	case functionStatusSucceeded:
		if !row.CatalogRevision.Valid || !row.InvocationOutcome.Valid ||
			row.InvocationOutcome.String != functionStatusSucceeded ||
			row.ReasonCode.Valid {
			return nil, functionCorruption("invalid success evidence")
		}
		return automations.FunctionSucceeded{
			CompletedAt:     at,
			CatalogRevision: adapters.CatalogID(row.CatalogRevision.String),
		}, nil
	case functionStatusInterrupted:
		if row.CatalogRevision.Valid || row.InvocationOutcome.Valid || !row.ReasonCode.Valid {
			return nil, functionCorruption("invalid interruption evidence")
		}
		return automations.FunctionInterrupted{CompletedAt: at, ReasonCode: row.ReasonCode.String}, nil
	case functionStatusFailed:
		return functionFailureFromRow(row, at)
	default:
		return nil, functionCorruption("unknown status")
	}
}

func functionFailureFromRow(
	row dbsqlc.AutomationRunFunction,
	at time.Time,
) (automations.FunctionExecutionState, error) {
	if !row.ReasonCode.Valid {
		return nil, functionCorruption("failure lacks reason")
	}
	if !row.CatalogRevision.Valid && !row.InvocationOutcome.Valid {
		return automations.FunctionFailed{CompletedAt: at,
			Evidence: automations.FunctionAdmissionRejected{ReasonCode: row.ReasonCode.String}}, nil
	}
	if !row.CatalogRevision.Valid || !row.InvocationOutcome.Valid {
		return nil, functionCorruption("partial invocation evidence")
	}
	var outcome adapters.Evidence
	switch row.InvocationOutcome.String {
	case functionStatusFailed:
		outcome = adapters.Failed{ReasonCode: row.ReasonCode.String}
	case "not_executed":
		outcome = adapters.NotExecuted{ReasonCode: row.ReasonCode.String}
	case "unknown":
		outcome = adapters.Unknown{ReasonCode: row.ReasonCode.String}
	default:
		return nil, functionCorruption("invalid failure outcome")
	}
	return automations.FunctionFailed{CompletedAt: at, Evidence: automations.FunctionInvocationFailed{
		CatalogRevision: adapters.CatalogID(row.CatalogRevision.String), Outcome: outcome,
	}}, nil
}

func functionCorruption(message string) error {
	return fmt.Errorf("%w: Function %s", automations.ErrInvalidAutomation, message)
}
