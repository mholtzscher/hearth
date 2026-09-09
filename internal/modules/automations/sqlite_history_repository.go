package automations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func automationRecord(row dbsqlc.Automation) (AutomationRecord, error) {
	record := AutomationRecord{
		ID:         AutomationID(row.ID),
		Revision:   row.Revision,
		Definition: AutomationDefinition{Name: row.Name, Enabled: row.Enabled == 1},
	}
	var triggers []automationTriggerJSON
	var steps []automationStepJSON
	if err := json.Unmarshal([]byte(row.TriggersJson), &triggers); err != nil {
		return record, fmt.Errorf("automation stored triggers: %w", err)
	}
	if err := json.Unmarshal([]byte(row.StepsJson), &steps); err != nil {
		return record, fmt.Errorf("automation stored steps: %w", err)
	}
	record.Definition.Triggers = make([]AutomationTrigger, len(triggers))
	record.Definition.Steps = make([]AutomationStep, len(steps))
	for i, t := range triggers {
		record.Definition.Triggers[i] = AutomationTrigger(t)
	}
	for i, s := range steps {
		record.Definition.Steps[i] = AutomationStep{
			EntityID:      s.EntityID,
			OperationName: s.OperationName,
			Parameters:    devices.CommandParameters(s.Parameters),
		}
	}
	var err error
	record.CreatedAt, err = time.Parse(automationTimestampLayout, row.CreatedAt)
	if err != nil {
		return record, err
	}
	record.UpdatedAt, err = time.Parse(automationTimestampLayout, row.UpdatedAt)
	return record, err
}

// GetAutomationRun returns immutable snapshots and currently owned command evidence.
func (repo *SQLiteRepository) GetAutomationRun(ctx context.Context, id AutomationRunID) (AutomationRunRecord, error) {
	var record AutomationRunRecord
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		row, err := q.GetAutomationRun(ctx, dbsqlc.GetAutomationRunParams{ID: string(id)})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAutomationRunNotFound
		}
		if err != nil {
			return err
		}
		record, err = readAutomationRun(ctx, q, row)
		return err
	})
	return record, err
}

// ListAutomationRuns keeps the filter historical and reads each page consistently.
func (repo *SQLiteRepository) ListAutomationRuns(
	ctx context.Context,
	input AutomationRunListParams,
) (AutomationPage[AutomationRunRecord], error) {
	page := AutomationPage[AutomationRunRecord]{Items: []AutomationRunRecord{}}
	limit, limitErr := automationPageLimit(input.Limit)
	if limitErr != nil {
		return page, limitErr
	}
	if (input.BeforeStartedAt == nil) != (input.BeforeID == nil) {
		return page, fmt.Errorf("%w: run cursor requires time and ID", ErrInvalidAutomation)
	}
	params := dbsqlc.ListAutomationRunsParams{
		AutomationFilter: automationNullableString(input.AutomationID),
		BeforeTime:       automationNullableTime(input.BeforeStartedAt),
		BeforeID:         automationNullableString(input.BeforeID),
		PageLimit:        int64(limit + 1),
	}
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		rows, err := q.ListAutomationRuns(ctx, params)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			page.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			record, readErr := decodeAutomationRunRow(row)
			if readErr != nil {
				return readErr
			}
			page.Items = append(page.Items, record)
		}
		return nil
	})
	return page, err
}

// decodeAutomationRunRow decodes only the automation_runs row. ListAutomationRuns
// uses this so summary pages never hydrate steps or command evidence that the
// HTTP summary discards.
func decodeAutomationRunRow(row dbsqlc.AutomationRun) (AutomationRunRecord, error) {
	record := AutomationRunRecord{
		ID:          AutomationRunID(row.ID),
		Source:      AutomationRunSource(row.Source),
		Status:      AutomationRunStatus(row.Status),
		FailureCode: automationPointer[string](row.FailureCode),
		Steps:       []AutomationRunStep{},
	}
	snapshot, snapshotErr := decodeAutomationRunSnapshot([]byte(row.SnapshotJson))
	if snapshotErr != nil {
		return record, fmt.Errorf("automation stored snapshot: %w", snapshotErr)
	}
	record.Snapshot = snapshot
	if err := json.Unmarshal([]byte(row.MatchedTriggerIdsJson), &record.MatchedTriggerIDs); err != nil {
		return record, fmt.Errorf("automation stored trigger matches: %w", err)
	}
	var err error
	record.StartedAt, err = time.Parse(automationTimestampLayout, row.StartedAt)
	if err != nil {
		return record, err
	}
	record.ScheduledAt, err = parseAutomationNullableTime(row.ScheduledAt)
	if err != nil {
		return record, err
	}
	record.CompletedAt, err = parseAutomationNullableTime(row.CompletedAt)
	if err != nil {
		return record, err
	}
	return record, nil
}

func readAutomationRun(ctx context.Context, q *dbsqlc.Queries, row dbsqlc.AutomationRun) (AutomationRunRecord, error) {
	record, err := decodeAutomationRunRow(row)
	if err != nil {
		return record, err
	}
	steps, err := q.ListAutomationRunSteps(ctx, dbsqlc.ListAutomationRunStepsParams{RunID: row.ID})
	if err != nil {
		return record, err
	}
	for _, rowStep := range steps {
		step, readErr := readAutomationStep(ctx, q, rowStep)
		if readErr != nil {
			return record, readErr
		}
		record.Steps = append(record.Steps, step)
	}
	return record, nil
}

func readAutomationStep(
	ctx context.Context,
	q *dbsqlc.Queries,
	row dbsqlc.AutomationRunStep,
) (AutomationRunStep, error) {
	step := AutomationRunStep{
		Index:                 int(row.StepIndex),
		Status:                AutomationStepStatus(row.Status),
		ReservedCommandID:     automationPointer[devices.CommandID](row.ReservedCommandID),
		ReservedCorrelationID: automationPointer[devices.CorrelationID](row.ReservedCorrelationID),
		FailureCode:           automationPointer[string](row.FailureCode),
		PrecreationFailure:    row.PrecreationFailure == 1,
	}
	var definition automationStepJSON
	if err := json.Unmarshal([]byte(row.DefinitionJson), &definition); err != nil {
		return step, fmt.Errorf("automation stored step: %w", err)
	}
	step.Definition = AutomationStep{
		EntityID:      definition.EntityID,
		OperationName: definition.OperationName,
		Parameters:    devices.CommandParameters(definition.Parameters),
	}
	var err error
	step.StartedAt, err = parseAutomationNullableTime(row.StartedAt)
	if err != nil {
		return step, err
	}
	step.CompletedAt, err = parseAutomationNullableTime(row.CompletedAt)
	if err != nil {
		return step, err
	}
	if step.ReservedCommandID == nil {
		return step, nil
	}
	candidate, err := q.GetAutomationCommandCandidate(
		ctx,
		dbsqlc.GetAutomationCommandCandidateParams{ID: string(*step.ReservedCommandID)},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return step, nil
	}
	if err != nil {
		return step, err
	}
	command := devices.CommandRecord{
		ID:            devices.CommandID(candidate.ID),
		CorrelationID: devices.CorrelationID(candidate.CorrelationID),
		Status:        devices.CommandStatus(candidate.Status),
	}
	if !AutomationStepOwnsCommand(step, command) {
		return step, nil
	}
	step.CommandID = &command.ID
	step.CommandStatus = &command.Status
	//exhaustive:ignore
	switch command.Status {
	case devices.CommandStatusSatisfied:
		outcome := devices.OutcomeObserved
		step.Outcome = &outcome
	case devices.CommandStatusDispatched:
		outcome := devices.OutcomeDispatched
		step.Outcome = &outcome
	}
	return step, nil
}
func automationPointer[T ~string](value sql.NullString) *T {
	if !value.Valid {
		return nil
	}
	result := T(value.String)
	return &result
}

//nolint:nilnil // SQL NULL is an absent optional timestamp, not an error.
func parseAutomationNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(automationTimestampLayout, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
