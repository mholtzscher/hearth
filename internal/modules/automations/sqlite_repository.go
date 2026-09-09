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

const automationTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// SQLiteRepository owns automation transactions on the core's single connection.
// Callers must never wrap these methods in device command transactions.
type SQLiteRepository struct {
	database        *sql.DB
	queries         *dbsqlc.Queries
	now             func() time.Time
	newAutomationID func() (AutomationID, error)
	newRunID        func() (AutomationRunID, error)
}

// NewSQLiteRepository uses the migrated core database; no background work is started.
func NewSQLiteRepository(database *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{
		database:        database,
		queries:         dbsqlc.New(database),
		now:             time.Now,
		newAutomationID: NewAutomationID,
		newRunID:        NewAutomationRunID,
	}
}
func (repo *SQLiteRepository) transaction(ctx context.Context, action func(*dbsqlc.Queries) error) error {
	tx, err := repo.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("automation transaction begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = action(repo.queries.WithTx(tx)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("automation transaction commit: %w", err)
	}
	return nil
}

// CreateAutomation persists a service-validated normalized definition at revision 1.
func (repo *SQLiteRepository) CreateAutomation(
	ctx context.Context,
	definition AutomationDefinition,
) (AutomationRecord, error) {
	var result AutomationRecord
	triggers, steps, prepareErr := encodeAutomationParts(definition)
	if prepareErr != nil {
		return result, prepareErr
	}
	id, prepareErr := repo.newAutomationID()
	if prepareErr != nil {
		return result, prepareErr
	}
	transactionErr := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		now := automationTime(repo.now())
		row, err := q.CreateAutomation(
			ctx,
			dbsqlc.CreateAutomationParams{
				ID:           string(id),
				Name:         definition.Name,
				Enabled:      automationBool(definition.Enabled),
				TriggersJson: triggers,
				StepsJson:    steps,
				CreatedAt:    now,
				UpdatedAt:    now,
			},
		)
		if err != nil {
			return err
		}
		result, err = automationRecord(row)
		return err
	})
	return result, transactionErr
}

// UpdateAutomation serializes revision replacement with admission and deletion.
func (repo *SQLiteRepository) UpdateAutomation(ctx context.Context, input AutomationUpdate) (AutomationRecord, error) {
	var result AutomationRecord
	triggers, steps, prepareErr := encodeAutomationParts(input.Definition)
	if prepareErr != nil {
		return result, prepareErr
	}
	transactionErr := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		if err := checkAutomationRevision(ctx, q, input.ID, input.ExpectedRevision); err != nil {
			return err
		}
		row, err := q.UpdateAutomation(
			ctx,
			dbsqlc.UpdateAutomationParams{
				ID:               string(input.ID),
				ExpectedRevision: input.ExpectedRevision,
				Name:             input.Definition.Name,
				Enabled:          automationBool(input.Definition.Enabled),
				TriggersJson:     triggers,
				StepsJson:        steps,
				UpdatedAt:        automationTime(repo.now()),
			},
		)
		if err != nil {
			return err
		}
		result, err = automationRecord(row)
		return err
	})
	return result, transactionErr
}

// DeleteAutomation preserves run history and retained keys; active claims conflict.
func (repo *SQLiteRepository) DeleteAutomation(ctx context.Context, id AutomationID, revision int64) error {
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		if err := checkAutomationRevision(ctx, q, id, revision); err != nil {
			return err
		}
		if err := checkAutomationInactive(ctx, q, id); err != nil {
			return err
		}
		count, err := q.DeleteAutomation(ctx, dbsqlc.DeleteAutomationParams{ID: string(id), Revision: revision})
		return automationTransition(count, err)
	})
}
func checkAutomationRevision(ctx context.Context, q *dbsqlc.Queries, id AutomationID, revision int64) error {
	row, err := q.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAutomationNotFound
	}
	if err != nil {
		return err
	}
	if revision <= 0 || row.Revision != revision {
		return ErrAutomationRevisionConflict
	}
	return nil
}
func checkAutomationInactive(ctx context.Context, q *dbsqlc.Queries, id AutomationID) error {
	_, err := q.GetActiveAutomationRun(ctx, dbsqlc.GetActiveAutomationRunParams{AutomationID: string(id)})
	if err == nil {
		return ErrAutomationRunActive
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// GetAutomation reads the current live definition, not historical snapshots.
func (repo *SQLiteRepository) GetAutomation(ctx context.Context, id AutomationID) (AutomationRecord, error) {
	row, err := repo.queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationRecord{}, ErrAutomationNotFound
	}
	if err != nil {
		return AutomationRecord{}, err
	}
	return automationRecord(row)
}

// ListAutomations fetches limit+1 in ascending canonical ID order.
func (repo *SQLiteRepository) ListAutomations(
	ctx context.Context,
	input AutomationListParams,
) (AutomationPage[AutomationRecord], error) {
	page := AutomationPage[AutomationRecord]{Items: []AutomationRecord{}}
	limit, err := automationPageLimit(input.Limit)
	if err != nil {
		return page, err
	}
	after := ""
	if input.AfterID != nil {
		after = string(*input.AfterID)
	}
	rows, err := repo.queries.ListAutomations(
		ctx,
		dbsqlc.ListAutomationsParams{AfterID: after, PageLimit: int64(limit + 1)},
	)
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		record, recordErr := automationRecord(row)
		if recordErr != nil {
			return page, recordErr
		}
		page.Items = append(page.Items, record)
	}
	return page, nil
}

// AdmitManualRun checks the retained key before consulting the live definition.
// The lifecycle gate must cover this commit and worker registration in the service.
func (repo *SQLiteRepository) AdmitManualRun(
	ctx context.Context,
	input AutomationManualAdmission,
) (AutomationAdmission, error) {
	var admission AutomationAdmission
	if err := ValidateAutomationIdempotencyKey(input.Request.IdempotencyKey); err != nil {
		return admission, err
	}
	if input.Timezone == "" {
		return admission, fmt.Errorf("%w: timezone required", ErrInvalidAutomation)
	}
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		row, err := q.GetManualAutomationRun(
			ctx,
			dbsqlc.GetManualAutomationRunParams{
				AutomationID:   string(input.Request.AutomationID),
				IdempotencyKey: automationString(input.Request.IdempotencyKey),
			},
		)
		if err == nil {
			admission.Reused = true
			admission.Run, err = readAutomationRun(ctx, q, row)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		live, err := q.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(input.Request.AutomationID)})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAutomationNotFound
		}
		if err != nil {
			return err
		}
		if err = checkAutomationInactive(ctx, q, input.Request.AutomationID); err != nil {
			return err
		}
		definition, err := automationRecord(live)
		if err != nil {
			return err
		}
		id, err := repo.newRunID()
		if err != nil {
			return err
		}
		run := AutomationRunRecord{
			ID: id,
			Snapshot: AutomationRunSnapshot{
				AutomationID: definition.ID,
				Revision:     definition.Revision,
				Definition:   definition.Definition,
				Timezone:     input.Timezone,
			},
			Source:            AutomationRunSourceManual,
			MatchedTriggerIDs: []AutomationTriggerID{},
			Status:            AutomationRunStatusRunning,
			StartedAt:         repo.now().UTC(),
		}
		if err = createAutomationRun(ctx, q, run, &input.Request.IdempotencyKey); err != nil {
			return err
		}
		saved, err := q.GetAutomationRun(ctx, dbsqlc.GetAutomationRunParams{ID: string(id)})
		if err != nil {
			return err
		}
		admission.Run, err = readAutomationRun(ctx, q, saved)
		return err
	})
	return admission, err
}

// createAutomationRun is shared transaction-local admission infrastructure for spec 2.
func createAutomationRun(ctx context.Context, q *dbsqlc.Queries, run AutomationRunRecord, key *string) error {
	if err := ValidateAutomationRunProvenance(run, key); err != nil {
		return err
	}
	snapshot, err := encodeAutomationRunSnapshot(run.Snapshot)
	if err != nil {
		return err
	}
	matches, err := json.Marshal(run.MatchedTriggerIDs)
	if err != nil {
		return err
	}
	err = q.CreateAutomationRun(
		ctx,
		dbsqlc.CreateAutomationRunParams{
			ID:                    string(run.ID),
			AutomationID:          string(run.Snapshot.AutomationID),
			Revision:              run.Snapshot.Revision,
			SnapshotJson:          string(snapshot),
			Source:                string(run.Source),
			ScheduledAt:           automationNullableTime(run.ScheduledAt),
			MatchedTriggerIdsJson: string(matches),
			StartedAt:             automationTime(run.StartedAt),
			IdempotencyKey:        automationNullableString(key),
		},
	)
	if err != nil {
		return err
	}
	for index, step := range run.Snapshot.Definition.Steps {
		raw, encodeErr := json.Marshal(
			automationStepJSON{
				EntityID:      step.EntityID,
				OperationName: step.OperationName,
				Parameters:    json.RawMessage(step.Parameters),
			},
		)
		if encodeErr != nil {
			return encodeErr
		}
		if err = q.CreateAutomationRunStep(
			ctx,
			dbsqlc.CreateAutomationRunStepParams{
				RunID:          string(run.ID),
				StepIndex:      int64(index),
				DefinitionJson: string(raw),
			},
		); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAutomationRunProvenance validates storage provenance, not calendar matching.
// Spec 2's evaluator must supply every matching ID in snapshot order.
func ValidateAutomationRunProvenance(run AutomationRunRecord, key *string) error {
	invalid := func() error { return fmt.Errorf("%w: run provenance", ErrInvalidAutomation) }
	if run.MatchedTriggerIDs == nil {
		return invalid()
	}
	switch run.Source {
	case AutomationRunSourceManual:
		if run.ScheduledAt != nil || len(run.MatchedTriggerIDs) != 0 || key == nil {
			return invalid()
		}
		return ValidateAutomationIdempotencyKey(*key)
	case AutomationRunSourceScheduled:
		if key != nil || run.ScheduledAt == nil || len(run.MatchedTriggerIDs) == 0 ||
			!run.ScheduledAt.Equal(run.ScheduledAt.Truncate(time.Minute)) {
			return invalid()
		}
		_, offset := run.ScheduledAt.Zone()
		if offset != 0 {
			return invalid()
		}
		return validateAutomationMatchedTriggerIDs(run)
	default:
		return invalid()
	}
}

func validateAutomationMatchedTriggerIDs(run AutomationRunRecord) error {
	invalid := func() error { return fmt.Errorf("%w: matched trigger IDs", ErrInvalidAutomation) }
	next := 0
	seen := map[AutomationTriggerID]bool{}
	for _, trigger := range run.Snapshot.Definition.Triggers {
		if seen[trigger.ID] {
			return invalid()
		}
		seen[trigger.ID] = true
		if next < len(run.MatchedTriggerIDs) && trigger.ID == run.MatchedTriggerIDs[next] {
			next++
		}
	}
	if next != len(run.MatchedTriggerIDs) {
		return invalid()
	}
	return nil
}

// BeginAutomationStep commits step intent only after every prior step succeeded.
func (repo *SQLiteRepository) BeginAutomationStep(ctx context.Context, input AutomationStepStart) error {
	if _, err := devices.ParseCommandID(string(input.CommandID)); err != nil {
		return fmt.Errorf("%w: reserved command ID", ErrInvalidAutomation)
	}
	if _, err := devices.ParseCorrelationID(string(input.CorrelationID)); err != nil {
		return fmt.Errorf("%w: reserved correlation ID", ErrInvalidAutomation)
	}
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		count, err := q.BeginAutomationStep(
			ctx,
			dbsqlc.BeginAutomationStepParams{
				RunID:                 string(input.RunID),
				StepIndex:             int64(input.Index),
				ReservedCommandID:     automationString(string(input.CommandID)),
				ReservedCorrelationID: automationString(string(input.CorrelationID)),
				StartedAt:             automationString(automationTime(repo.now())),
			},
		)
		return automationTransition(count, err)
	})
}

// CompleteAutomationStep persists established results before any later admission.
func (repo *SQLiteRepository) CompleteAutomationStep(ctx context.Context, input AutomationStepCompletion) error {
	if input.Status != AutomationStepStatusSatisfied && input.Status != AutomationStepStatusDispatched &&
		input.Status != AutomationStepStatusFailed {
		return ErrAutomationTransitionConflict
	}
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		now := automationString(automationTime(repo.now()))
		count, err := q.CompleteAutomationStep(
			ctx,
			dbsqlc.CompleteAutomationStepParams{
				RunID:       string(input.RunID),
				StepIndex:   int64(input.Index),
				Status:      string(input.Status),
				Outcome:     automationNullableString(input.Outcome),
				FailureCode: automationNullableString(input.FailureCode),
				CompletedAt: now,
			},
		)
		if err = automationTransition(count, err); err != nil {
			return err
		}
		if input.Status == AutomationStepStatusFailed {
			if err = q.SkipPendingAutomationSteps(
				ctx,
				dbsqlc.SkipPendingAutomationStepsParams{RunID: string(input.RunID), CompletedAt: now},
			); err != nil {
				return err
			}
			count, err = q.CompleteAutomationRun(
				ctx,
				dbsqlc.CompleteAutomationRunParams{
					ID:          string(input.RunID),
					Status:      string(AutomationRunStatusFailed),
					FailureCode: automationNullableString(input.FailureCode),
					CompletedAt: now,
				},
			)
			return automationTransition(count, err)
		}
		return nil
	})
}

// CompleteAutomationRun succeeds only fully completed sequences, or interrupts a
// known stopped sequence. Never use it to terminalize uncertain executor faults.
func (repo *SQLiteRepository) CompleteAutomationRun(ctx context.Context, input AutomationRunCompletion) error {
	return repo.transaction(
		ctx,
		func(q *dbsqlc.Queries) error { return completeAutomationRun(ctx, q, input, repo.now()) },
	)
}
func completeAutomationRun(ctx context.Context, q *dbsqlc.Queries, input AutomationRunCompletion, now time.Time) error {
	if input.Status != AutomationRunStatusSucceeded && input.Status != AutomationRunStatusInterrupted {
		return ErrAutomationTransitionConflict
	}
	steps, err := q.ListAutomationRunSteps(ctx, dbsqlc.ListAutomationRunStepsParams{RunID: string(input.RunID)})
	if err != nil {
		return err
	}
	if input.Status == AutomationRunStatusSucceeded {
		if !automationStepsSucceeded(steps) {
			return ErrAutomationTransitionConflict
		}
	} else {
		if err = q.InterruptRunningAutomationSteps(
			ctx,
			dbsqlc.InterruptRunningAutomationStepsParams{
				RunID:       string(input.RunID),
				FailureCode: automationNullableString(input.FailureCode),
				CompletedAt: automationString(automationTime(now)),
			},
		); err != nil {
			return err
		}
		if err = q.SkipPendingAutomationSteps(
			ctx,
			dbsqlc.SkipPendingAutomationStepsParams{
				RunID:       string(input.RunID),
				CompletedAt: automationString(automationTime(now)),
			},
		); err != nil {
			return err
		}
	}
	count, err := q.CompleteAutomationRun(
		ctx,
		dbsqlc.CompleteAutomationRunParams{
			ID:          string(input.RunID),
			Status:      string(input.Status),
			CompletedAt: automationString(automationTime(now)),
			FailureCode: automationNullableString(input.FailureCode),
		},
	)
	return automationTransition(count, err)
}

// InterruptAutomationRuns is startup-only recovery after device command recovery.
// All active claims, including fault-retained runs, are interrupted without replay.
// Owned command results remain available through the same history predicate.
func (repo *SQLiteRepository) InterruptAutomationRuns(ctx context.Context) error {
	return repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		rows, err := q.ListRunningAutomationRuns(ctx)
		if err != nil {
			return err
		}
		now := repo.now()
		code := AutomationFailureCoreRestarted
		for _, row := range rows {
			if err = completeAutomationRun(
				ctx,
				q,
				AutomationRunCompletion{
					RunID:       AutomationRunID(row.ID),
					Status:      AutomationRunStatusInterrupted,
					FailureCode: &code,
				},
				now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneAutomationHistory deletes at most 500 terminal runs strictly before cutoff.
// Cascading steps and the run's own key are removed in the same transaction.
func (repo *SQLiteRepository) PruneAutomationHistory(ctx context.Context, cutoff time.Time) (int64, error) {
	var count int64
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		var err error
		count, err = q.PruneAutomationHistory(
			ctx,
			dbsqlc.PruneAutomationHistoryParams{Cutoff: automationString(automationTime(cutoff))},
		)
		return err
	})
	return count, err
}
func automationTransition(count int64, err error) error {
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAutomationTransitionConflict
	}
	return nil
}
func automationTime(value time.Time) string        { return value.UTC().Format(automationTimestampLayout) }
func automationString(value string) sql.NullString { return sql.NullString{String: value, Valid: true} }
func automationNullableString[T ~string](value *T) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return automationString(string(*value))
}
func automationNullableTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return automationString(automationTime(*value))
}
func automationBool(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
func automationPageLimit(limit int) (int, error) {
	if limit == 0 {
		const defaultAutomationPageLimit = 50
		return defaultAutomationPageLimit, nil
	}
	if limit < 1 || limit > 200 {
		return 0, fmt.Errorf("%w: page limit", ErrInvalidAutomation)
	}
	return limit, nil
}
func encodeAutomationParts(definition AutomationDefinition) (string, string, error) {
	raw, err := encodeAutomationDefinition(definition)
	if err != nil {
		return "", "", err
	}
	var value automationDefinitionJSON
	if err = json.Unmarshal(raw, &value); err != nil {
		return "", "", err
	}
	triggers, err := json.Marshal(value.Triggers)
	if err != nil {
		return "", "", err
	}
	steps, err := json.Marshal(value.Steps)
	return string(triggers), string(steps), err
}

func automationStepsSucceeded(steps []dbsqlc.AutomationRunStep) bool {
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if step.Status != string(AutomationStepStatusSatisfied) &&
			step.Status != string(AutomationStepStatusDispatched) {
			return false
		}
	}
	return true
}
