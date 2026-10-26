package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

type executionParent struct {
	snapshot automations.Definition
	matches  []automations.TriggerID
}

func runningExecutionParent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
) (executionParent, error) {
	row, err := queries.GetExecutionParent(ctx, dbsqlc.GetExecutionParentParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return executionParent{}, lifecycleCorruption("missing Step parent")
	}
	if err != nil {
		return executionParent{}, err
	}
	if row.Kind != "run" || row.RunStatus.String != "running" || !row.DefinitionJson.Valid {
		return executionParent{}, lifecycleCorruption("Step parent is not a running Run with retained configuration")
	}
	snapshot, err := automations.DecodeDefinition(json.RawMessage(row.DefinitionJson.String))
	if err != nil {
		return executionParent{}, err
	}
	matches, err := decodeTriggerIDs(json.RawMessage(row.RunMatchedTriggerIdsJson.String))
	return executionParent{snapshot: snapshot, matches: matches}, err
}

// RecordStep reserves a reached execution and its cross-kind order in one transaction.
func (repo *AutomationRepository) RecordStep(ctx context.Context, record automations.StepExecutionRecord) error {
	body, err := automations.RecordStepBody(record)
	if err != nil {
		return err
	}
	envelope, err := automations.EncodeStepExecutionBody(body)
	if err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, parentErr := runningExecutionParent(ctx, queries, record.RunID)
		if parentErr != nil {
			return parentErr
		}
		controls, readErr := queries.ListStepExecutionControl(
			ctx,
			dbsqlc.ListStepExecutionControlParams{RunID: string(record.RunID)},
		)
		if readErr != nil {
			return readErr
		}
		step, controlErr := nextExecutionStep(parent.snapshot.Steps, controls)
		if controlErr != nil {
			return controlErr
		}
		if step == nil || step.ID != record.StepID ||
			string(automations.StepBodyKind(step.Body)) != string(envelope.Kind) {
			return lifecycleCorruption("record does not name the next selected Step")
		}
		if branch, ok := body.(automations.BranchExecutionBody); ok {
			if branchErr := automations.ValidateBranchDecisionWithPreparedSnapshot(
				branch.Decision,
				parent.snapshot,
				parent.matches,
			); branchErr != nil {
				return branchErr
			}
		}
		if insertErr := queries.CreateStepExecution(ctx, dbsqlc.CreateStepExecutionParams{
			RunID: string(
				record.RunID,
			),
			StepID: string(record.StepID),
			Kind:   string(envelope.Kind),
			Phase:  envelope.Phase,
			StartedAt: encodeAutomationTimestamp(
				envelope.StartedAt,
			),
			CompletedAt:  executionTime(envelope.CompletedAt),
			EvidenceJson: string(envelope.EvidenceJSON),
		}); insertErr != nil {
			return insertErr
		}
		return terminalizeExecutionParent(ctx, queries, record.RunID, body, envelope.CompletedAt)
	})
}

// CompleteStep validates the target reservation and atomically records terminal Step/Run evidence.
func (repo *AutomationRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepExecutionCompletion,
) error {
	if _, err := automations.ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if _, err := automations.ParseStepID(string(completion.StepID)); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, err := runningExecutionParent(ctx, queries, completion.RunID)
		if err != nil {
			return err
		}
		execution, err := executionCompletionTarget(ctx, queries, parent, completion)
		if err != nil {
			return err
		}
		body, err := automations.CompleteStepBody(execution.Body, completion)
		if err != nil {
			return err
		}
		envelope, err := automations.EncodeStepExecutionBody(body)
		if err != nil {
			return err
		}
		changed, err := queries.CompleteStepExecution(ctx, dbsqlc.CompleteStepExecutionParams{
			RunID: string(completion.RunID), StepID: string(completion.StepID), Phase: envelope.Phase,
			CompletedAt: executionTime(envelope.CompletedAt), EvidenceJson: string(envelope.EvidenceJSON),
		})
		if err != nil {
			return err
		}
		if changed != 1 {
			return lifecycleCorruption("Step is not running")
		}
		return terminalizeExecutionParent(ctx, queries, completion.RunID, body, envelope.CompletedAt)
	})
}

func terminalizeExecutionParent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	body automations.StepExecutionBody,
	at *time.Time,
) error {
	outcome, failed := automations.ParentFailure(body)
	if !failed {
		return nil
	}
	if at == nil {
		return lifecycleCorruption("terminal Step has no completion time")
	}
	return completeExecutionRun(ctx, queries, id, outcome, *at)
}

func completeExecutionRun(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
	outcome automations.RunOutcome,
	at time.Time,
) error {
	changed, err := queries.CompleteExecutionParent(ctx, dbsqlc.CompleteExecutionParentParams{
		RunID:  string(id),
		Status: sql.NullString{String: string(automations.RunOutcomeStatus(outcome)), Valid: true},
		Reason: encodeNullableString(
			automations.RunOutcomeFailureCode(outcome),
		),
		CompletedAt: sql.NullString{String: encodeAutomationTimestamp(at), Valid: true},
	})
	if err != nil {
		return err
	}
	if changed != 1 {
		return lifecycleCorruption("Run is not completable")
	}
	return nil
}

func executionTime(at *time.Time) sql.NullString {
	if at == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: encodeAutomationTimestamp(*at), Valid: true}
}

func executionFromRow(row dbsqlc.AutomationStepExecution) (automations.StepExecution, error) {
	id, err := automations.ParseStepID(row.StepID)
	if err != nil {
		return automations.StepExecution{}, err
	}
	started, err := decodeAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.StepExecution{}, err
	}
	completed, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.StepExecution{}, err
	}
	body, err := automations.DecodeStepExecutionBody(id, automations.StepExecutionEnvelope{
		Kind: automations.StepKind(
			row.Kind,
		),
		Phase:        row.Phase,
		StartedAt:    started,
		CompletedAt:  completed,
		EvidenceJSON: json.RawMessage(row.EvidenceJson),
	})
	return automations.StepExecution{StepID: id, Sequence: int(row.Sequence), Body: body}, err
}

func runExecutions(ctx context.Context, queries *dbsqlc.Queries, runID string) ([]automations.StepExecution, error) {
	rows, err := queries.ListStepExecutions(ctx, dbsqlc.ListStepExecutionsParams{RunID: runID})
	if err != nil {
		return nil, err
	}
	trace := make([]automations.StepExecution, 0, len(rows))
	for _, row := range rows {
		execution, decodeErr := executionFromRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		trace = append(trace, execution)
	}
	return trace, nil
}

func executionCompletionTarget(
	ctx context.Context,
	queries *dbsqlc.Queries,
	parent executionParent,
	completion automations.StepExecutionCompletion,
) (automations.StepExecution, error) {
	row, err := queries.GetStepExecution(
		ctx,
		dbsqlc.GetStepExecutionParams{RunID: string(completion.RunID), StepID: string(completion.StepID)},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return automations.StepExecution{}, lifecycleCorruption("completion target is absent")
	}
	if err != nil {
		return automations.StepExecution{}, err
	}
	step, err := automations.StepWithPreparedSnapshot(parent.snapshot, completion.StepID)
	if err != nil {
		return automations.StepExecution{}, err
	}
	if automations.StepBodyKind(step.Body) != automations.StepKind(row.Kind) {
		return automations.StepExecution{}, lifecycleCorruption("completion disagrees with retained Step")
	}
	return executionFromRow(row)
}

// nextExecutionStep reads only control labels, not previous outcome/evaluation documents.
func nextExecutionStep(
	steps []automations.Step,
	controls []dbsqlc.ListStepExecutionControlRow,
) (*automations.Step, error) {
	cursor := executionControlCursor{rows: controls}
	step, err := cursor.next(steps)
	if err != nil {
		return nil, err
	}
	if cursor.position != len(controls) {
		return nil, lifecycleCorruption("unreachable execution controls")
	}
	return step, nil
}

type executionControlCursor struct {
	rows     []dbsqlc.ListStepExecutionControlRow
	position int
}

func (cursor *executionControlCursor) next(steps []automations.Step) (*automations.Step, error) {
	for _, step := range steps {
		if cursor.position == len(cursor.rows) {
			return &step, nil
		}
		row := cursor.rows[cursor.position]
		if row.Sequence != int64(cursor.position) || row.StepID != string(step.ID) ||
			row.Kind != string(automations.StepBodyKind(step.Body)) {
			return nil, lifecycleCorruption("execution control does not follow selected path")
		}
		cursor.position++
		if row.Phase != "succeeded" {
			return nil, lifecycleCorruption("prior Step has not succeeded")
		}
		children, err := executionControlChildren(step, row)
		if err != nil {
			return nil, err
		}
		if children != nil {
			next, childErr := cursor.next(children)
			if childErr != nil || next != nil {
				return next, childErr
			}
		}
	}
	return nil, nil //nolint:nilnil // Exhausting the selected path has no next Step.
}

func executionControlChildren(
	step automations.Step,
	row dbsqlc.ListStepExecutionControlRow,
) ([]automations.Step, error) {
	outcome, _ := row.BranchOutcome.(string)
	switch body := step.Body.(type) {
	case automations.IfStep:
		switch automations.BranchOutcome(outcome) {
		case automations.BranchThen:
			return body.Then, nil
		case automations.BranchElse:
			return body.Else, nil
		case automations.BranchNoMatch:
			if len(body.Else) != 0 {
				return nil, lifecycleCorruption("If no-match omits Else")
			}
			return []automations.Step{}, nil
		case automations.BranchChosen, automations.BranchDefault, automations.BranchUnknown, automations.BranchError:
			return nil, lifecycleCorruption("invalid If selection")
		default:
			return nil, lifecycleCorruption("unknown If selection")
		}
	case automations.ChooseStep:
		return chooseControlChildren(body, row, automations.BranchOutcome(outcome))
	case automations.CommandStep, automations.DelayStep, automations.NotificationStep, automations.FunctionStep:
		return []automations.Step{}, nil
	default:
		return nil, lifecycleCorruption("unknown Step representation")
	}
}

func chooseControlChildren(
	body automations.ChooseStep,
	row dbsqlc.ListStepExecutionControlRow,
	outcome automations.BranchOutcome,
) ([]automations.Step, error) {
	switch outcome {
	case automations.BranchDefault:
		return body.Default, nil
	case automations.BranchNoMatch:
		if len(body.Default) != 0 {
			return nil, lifecycleCorruption("Choose no-match omits Default")
		}
		return []automations.Step{}, nil
	case automations.BranchChosen:
		selected, _ := row.SelectedBranchID.(string)
		for _, branch := range body.Branches {
			if string(branch.ID) == selected {
				return branch.Steps, nil
			}
		}
		return nil, lifecycleCorruption("unknown Choose selection")
	case automations.BranchThen, automations.BranchElse, automations.BranchUnknown, automations.BranchError:
		return nil, lifecycleCorruption("invalid Choose selection")
	default:
		return nil, lifecycleCorruption("unknown Choose selection")
	}
}
