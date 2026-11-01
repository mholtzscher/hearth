package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func validateSubAutomationInput(input automations.SubAutomationStepInput, at time.Time) error {
	if _, err := automations.ParseRunID(string(input.ParentRunID)); err != nil {
		return err
	}
	if _, err := automations.ParseStepID(string(input.StepID)); err != nil {
		return err
	}
	_, offset := at.Zone()
	if at.IsZero() || offset != 0 {
		return errors.New("SubAutomation time must be UTC")
	}
	return nil
}

//nolint:gocognit,funlen // Atomic admission keeps all ordered eligibility checks together.
func (repo *AutomationRepository) AdmitSubAutomationRun(
	ctx context.Context,
	input automations.SubAutomationStepInput,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (*automations.Run, error) {
	if err := validateSubAutomationInput(input, at); err != nil {
		return nil, err
	}
	var result *automations.Run
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		parent, err := runningExecutionParent(ctx, q, input.ParentRunID)
		if err != nil {
			return err
		}
		if err = parent.requireInitializedValues(); err != nil {
			return err
		}
		controls, err := q.ListStepExecutionControl(
			ctx,
			dbsqlc.ListStepExecutionControlParams{RunID: string(input.ParentRunID)},
		)
		if err != nil {
			return err
		}
		step, err := nextExecutionStep(parent.snapshot.Steps, controls)
		if err != nil {
			return err
		}
		if step == nil || step.ID != input.StepID {
			return lifecycleCorruption("SubAutomation is not next selected Step")
		}
		subAutomation, ok := step.Body.(automations.SubAutomationStep)
		if !ok {
			return lifecycleCorruption("selected Step is not SubAutomation")
		}
		cause := automations.InvocationCause{
			ParentAutomationID: parent.automationID,
			ParentRunID:        input.ParentRunID,
			ParentStepID:       input.StepID,
		}
		row, err := q.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(subAutomation.AutomationID)})
		if errors.Is(err, sql.ErrNoRows) {
			return insertSubAutomation(
				ctx,
				q,
				input,
				at,
				automations.SubAutomationRejected{CompletedAt: at, ReasonCode: "automation_not_found"},
			)
		}
		if err != nil {
			return err
		}
		record, err := automationRecordFromRow(row)
		if err != nil {
			return err
		}
		busy, err := q.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{AutomationID: string(record.ID)})
		if err != nil {
			return err
		}
		if busy > 0 {
			return insertSubAutomation(
				ctx,
				q,
				input,
				at,
				automations.SubAutomationRejected{CompletedAt: at, ReasonCode: "automation_busy"},
			)
		}
		decision, blocked, err := manualConditionDecision(record.Definition.Conditions, false, snapshot, at)
		if err != nil {
			return err
		}
		if blocked != "" {
			id, idErr := repo.newSkipID()
			if idErr != nil {
				return idErr
			}
			skip := automations.Skip{
				ID:                id,
				AutomationID:      record.ID,
				AutomationName:    record.Definition.Name,
				Revision:          record.Revision,
				Cause:             cause,
				MatchedTriggers:   []automations.Trigger{},
				Reason:            blocked,
				ConditionDecision: decision,
				SkippedAt:         at,
			}
			if err = repo.persistHistorySkip(ctx, q, skip); err != nil {
				return err
			}
			return insertSubAutomation(
				ctx,
				q,
				input,
				at,
				automations.SubAutomationRejected{CompletedAt: at, ReasonCode: string(blocked), SkipID: &id},
			)
		}
		id, err := repo.newRunID()
		if err != nil {
			return err
		}
		run := automations.NewRunSnapshot(record, id, cause, nil, decision, at)
		if err = repo.persistRun(ctx, q, run); err != nil {
			return err
		}
		if err = insertSubAutomation(ctx, q, input, at, automations.SubAutomationRunning{ChildRunID: id}); err != nil {
			return err
		}
		result = &run
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func insertSubAutomation(
	ctx context.Context,
	q *dbsqlc.Queries,
	input automations.SubAutomationStepInput,
	at time.Time,
	state automations.SubAutomationExecutionState,
) error {
	details := automations.SubAutomationExecutionDetails{StartedAt: at, State: state}
	env, err := automations.EncodeStepExecutionDetails(details)
	if err != nil {
		return err
	}
	if err = q.CreateStepExecution(
		ctx,
		dbsqlc.CreateStepExecutionParams{
			RunID:  string(input.ParentRunID),
			StepID: string(input.StepID),
			Kind: string(
				env.Kind,
			),
			Phase:        env.Phase,
			StartedAt:    encodeAutomationTimestamp(at),
			CompletedAt:  executionTime(env.CompletedAt),
			EvidenceJson: string(env.EvidenceJSON),
		},
	); err != nil {
		return err
	}
	return terminalizeExecutionParent(ctx, q, input.ParentRunID, details, env.CompletedAt)
}

//nolint:gocognit,gocyclo,cyclop,funlen // Linked outcome verification and transition form one transaction.
func (repo *AutomationRepository) CompleteSubAutomationStep(
	ctx context.Context,
	input automations.SubAutomationStepInput,
	at time.Time,
) (automations.SubAutomationExecutionDetails, error) {
	if err := validateSubAutomationInput(input, at); err != nil {
		return automations.SubAutomationExecutionDetails{}, err
	}
	var result automations.SubAutomationExecutionDetails
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		parent, err := runningExecutionParent(ctx, q, input.ParentRunID)
		if err != nil {
			return err
		}
		if err = parent.requireInitializedValues(); err != nil {
			return err
		}
		step, err := automations.StepWithPreparedSnapshot(parent.snapshot, input.StepID)
		if err != nil {
			return err
		}
		target, ok := step.Body.(automations.SubAutomationStep)
		if !ok {
			return lifecycleCorruption("SubAutomation target is not SubAutomation")
		}
		execution, err := executionCompletionTarget(
			ctx,
			q,
			parent,
			automations.StepExecutionCompletion{RunID: input.ParentRunID, StepID: input.StepID},
		)
		if err != nil {
			return err
		}
		details, ok := execution.Details.(automations.SubAutomationExecutionDetails)
		if !ok {
			return lifecycleCorruption("SubAutomation execution has wrong kind")
		}
		running, ok := details.State.(automations.SubAutomationRunning)
		if !ok {
			return lifecycleCorruption("SubAutomation is not running")
		}
		child, err := q.GetSubAutomationChildOutcome(
			ctx,
			dbsqlc.GetSubAutomationChildOutcomeParams{ID: string(running.ChildRunID)},
		)
		if err != nil {
			return err
		}
		if child.Kind != "run" || child.AutomationID != string(target.AutomationID) ||
			!child.RunSource.Valid || child.RunSource.String != "invocation" ||
			!child.ParentAutomationID.Valid ||
			child.ParentAutomationID.String != string(parent.automationID) ||
			!child.ParentRunID.Valid ||
			child.ParentRunID.String != string(input.ParentRunID) ||
			!child.ParentStepID.Valid ||
			child.ParentStepID.String != string(input.StepID) ||
			!child.RunStatus.Valid ||
			!child.RunCompletedAt.Valid {
			return lifecycleCorruption("SubAutomation child outcome mismatches parent")
		}
		if _, err = decodeAutomationTimestamp(child.RunCompletedAt.String); err != nil {
			return err
		}
		switch automations.RunStatus(child.RunStatus.String) {
		case automations.RunSucceeded:
			if child.RunFailureCode.Valid {
				return lifecycleCorruption("successful child has failure")
			}
			details.State = automations.SubAutomationSucceeded{ChildRunID: running.ChildRunID, CompletedAt: at}
		case automations.RunFailed, automations.RunInterrupted:
			if !child.RunFailureCode.Valid || child.RunFailureCode.String == "" {
				return lifecycleCorruption("child failure lacks code")
			}
			var outcome automations.RunOutcome = automations.FailedRun{FailureCode: child.RunFailureCode.String}
			if child.RunStatus.String == string(automations.RunInterrupted) {
				outcome = automations.InterruptedRun{FailureCode: child.RunFailureCode.String}
			}
			details.State = automations.SubAutomationChildFailed{
				ChildRunID:  running.ChildRunID,
				CompletedAt: at,
				Outcome:     outcome,
			}
		case automations.RunRunning:
			return lifecycleCorruption("SubAutomation child is not terminal")
		default:
			return lifecycleCorruption("SubAutomation child is not terminal")
		}
		env, err := automations.EncodeStepExecutionDetails(details)
		if err != nil {
			return err
		}
		changed, err := q.CompleteStepExecution(
			ctx,
			dbsqlc.CompleteStepExecutionParams{
				RunID:        string(input.ParentRunID),
				StepID:       string(input.StepID),
				Phase:        env.Phase,
				CompletedAt:  executionTime(env.CompletedAt),
				EvidenceJson: string(env.EvidenceJSON),
			},
		)
		if err != nil {
			return err
		}
		if changed != 1 {
			return lifecycleCorruption("SubAutomation is not running")
		}
		if err = terminalizeExecutionParent(ctx, q, input.ParentRunID, details, env.CompletedAt); err != nil {
			return err
		}
		result = details
		return nil
	})
	return result, err
}
