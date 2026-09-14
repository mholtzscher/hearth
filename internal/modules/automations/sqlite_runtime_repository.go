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

// compile-time proof that the SQLite adapter satisfies the complete
// domain-oriented persistence seam, including admission, execution, and history.
var _ AutomationRepository = (*SQLiteRepository)(nil)

// AdmitDeviceFact evaluates one Device Fact against every current enabled
// definition in one transaction. It commits each matching Automation's receipt,
// Run or Skip explanation, and initial Step rows together, and it registers no
// workers: the caller starts them only after the commit returns.
func (repo *SQLiteRepository) AdmitDeviceFact(
	ctx context.Context,
	fact DeviceFact,
	admittedAt time.Time,
) (AdmissionResult, error) {
	if err := ValidateDeviceFact(fact); err != nil {
		return AdmissionResult{}, err
	}
	if admittedAt.IsZero() {
		return AdmissionResult{}, invalidFact("admission time is required")
	}
	summary := factSummary(fact)
	var result AdmissionResult
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		rows, err := queries.ListAllAutomations(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = repo.admitAutomationRow(ctx, queries, row, fact, summary, admittedAt, &result); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AdmissionResult{}, err
	}
	return result, nil
}

// admitAutomationRow evaluates one current definition and folds its single
// outcome into the admission result.
func (repo *SQLiteRepository) admitAutomationRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.Automation,
	fact DeviceFact,
	summary DeviceFactSummary,
	admittedAt time.Time,
	result *AdmissionResult,
) error {
	record, err := automationRecord(row)
	if err != nil {
		return err
	}
	if !record.Definition.Enabled {
		return nil
	}
	matched, err := matchAutomationTriggers(fact, record.Definition)
	if err != nil {
		return err
	}
	if len(matched) == 0 {
		return nil
	}
	result.Outcome.MatchedAutomations++
	run, skip, err := repo.admitMatchedDeviceFact(ctx, queries, record, summary, matched, admittedAt)
	if err != nil {
		return err
	}
	switch {
	case run != nil:
		result.Outcome.StartedRuns++
		result.StartedRuns = append(result.StartedRuns, *run)
	case skip != nil:
		result.Outcome.RecordedSkips++
		result.Skips = append(result.Skips, *skip)
	default:
		result.Outcome.DuplicateOutcomes++
	}
	return nil
}

// admitMatchedDeviceFact resolves one already-matched Automation inside the
// admission transaction. It reports a started Run, a recorded Skip, or neither
// for a duplicate receipt. Freshness precedes the busy check, so an old Fact
// always explains itself as stale_fact.
func (repo *SQLiteRepository) admitMatchedDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record AutomationRecord,
	summary DeviceFactSummary,
	matched []TriggerID,
	admittedAt time.Time,
) (*AutomationRun, *AdmissionSkip, error) {
	receipts, err := queries.CountFactReceipts(ctx, dbsqlc.CountFactReceiptsParams{
		FactID:       string(summary.FactID),
		AutomationID: string(record.ID),
	})
	if err != nil {
		return nil, nil, err
	}
	if receipts > 0 {
		return nil, nil, nil
	}
	running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{
		AutomationID: string(record.ID),
	})
	if err != nil {
		return nil, nil, err
	}
	if admittedAt.Sub(summary.EmittedAt) > AutomationFactMaximumAge {
		skip, recordErr := repo.recordSkip(
			ctx, queries, record, summary, matched, AutomationSkipStaleFact, admittedAt,
		)
		if recordErr != nil {
			return nil, nil, recordErr
		}
		return nil, &skip, nil
	}
	if running > 0 {
		skip, recordErr := repo.recordSkip(
			ctx, queries, record, summary, matched, AutomationSkipBusy, admittedAt,
		)
		if recordErr != nil {
			return nil, nil, recordErr
		}
		return nil, &skip, nil
	}
	run, err := repo.buildRun(record, RunSourceDeviceFact, &summary, matched, admittedAt)
	if err != nil {
		return nil, nil, err
	}
	if err = repo.persistRun(ctx, queries, run); err != nil {
		return nil, nil, err
	}
	if err = repo.writeReceipt(
		ctx, queries, summary.FactID, record.ID, AutomationHistoryRun, string(run.ID),
	); err != nil {
		return nil, nil, err
	}
	return &run, nil, nil
}

// AdmitManualRun starts one Run from the current definition snapshot even when
// the Automation is disabled. A running Run for the same Automation returns
// [ErrAutomationBusy]; a missing definition returns [ErrAutomationNotFound].
func (repo *SQLiteRepository) AdmitManualRun(
	ctx context.Context,
	id AutomationID,
	admittedAt time.Time,
) (AutomationRun, error) {
	if _, err := ParseAutomationID(string(id)); err != nil {
		return AutomationRun{}, err
	}
	if admittedAt.IsZero() {
		return AutomationRun{}, fmt.Errorf("%w: admission time is required", ErrInvalidAutomation)
	}
	var run AutomationRun
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAutomationNotFound
		}
		if err != nil {
			return err
		}
		record, err := automationRecord(row)
		if err != nil {
			return err
		}
		running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{
			AutomationID: string(record.ID),
		})
		if err != nil {
			return err
		}
		if running > 0 {
			return ErrAutomationBusy
		}
		run, err = repo.buildRun(record, RunSourceManual, nil, nil, admittedAt)
		if err != nil {
			return err
		}
		return repo.persistRun(ctx, queries, run)
	})
	if err != nil {
		return AutomationRun{}, err
	}
	return run, nil
}

// MarkStepRunning durably reserves one Step's Command identity before the
// external call. The update only matches a still not-attempted Step.
func (repo *SQLiteRepository) MarkStepRunning(ctx context.Context, start StepStart) error {
	if _, err := ParseAutomationRunID(string(start.RunID)); err != nil {
		return err
	}
	if _, err := devices.ParseCommandID(string(start.CommandID)); err != nil {
		return fmt.Errorf("%w: reserved command ID: %w", ErrInvalidAutomation, err)
	}
	if _, err := devices.ParseCorrelationID(string(start.CorrelationID)); err != nil {
		return fmt.Errorf("%w: reserved correlation ID: %w", ErrInvalidAutomation, err)
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		updated, err := queries.MarkStepRunning(ctx, dbsqlc.MarkStepRunningParams{
			ReservedCommandID:     sql.NullString{String: string(start.CommandID), Valid: true},
			ReservedCorrelationID: sql.NullString{String: string(start.CorrelationID), Valid: true},
			StartedAt:             sql.NullString{String: encodeAutomationTimestamp(repo.now()), Valid: true},
			RunID:                 string(start.RunID),
			Position:              int64(start.Position),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("%w: step %s/%d is not startable", ErrInvalidAutomation, start.RunID, start.Position)
		}
		return nil
	})
}

// CompleteStep records one Step's established terminal outcome. A Step that
// never started but is interrupted before its Command is created keeps its
// started_at empty and receives one at completion.
func (repo *SQLiteRepository) CompleteStep(ctx context.Context, completion StepCompletion) error {
	if _, err := ParseAutomationRunID(string(completion.RunID)); err != nil {
		return err
	}
	if err := validateStepCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		completedAt := encodeAutomationTimestamp(repo.now())
		updated, err := queries.CompleteStep(ctx, dbsqlc.CompleteStepParams{
			Status:            string(completion.Status),
			VerifiedCommandID: encodeNullableString(commandIDString(completion.VerifiedCommandID)),
			FailureCode:       encodeNullableString(completion.FailureCode),
			StartedAt:         sql.NullString{String: completedAt, Valid: true},
			CompletedAt:       sql.NullString{String: completedAt, Valid: true},
			RunID:             string(completion.RunID),
			Position:          int64(completion.Position),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf(
				"%w: step %s/%d is not completable", ErrInvalidAutomation, completion.RunID, completion.Position,
			)
		}
		return nil
	})
}

// CompleteRun records one Run's established terminal state exactly once.
func (repo *SQLiteRepository) CompleteRun(ctx context.Context, completion RunCompletion) error {
	if _, err := ParseAutomationRunID(string(completion.RunID)); err != nil {
		return err
	}
	switch completion.Status {
	case RunSucceeded:
		if completion.FailureCode != nil {
			return fmt.Errorf("%w: succeeded Run carries a failure code", ErrInvalidAutomation)
		}
	case RunFailed, RunInterrupted:
		if completion.FailureCode == nil {
			return fmt.Errorf("%w: failing Run requires a failure code", ErrInvalidAutomation)
		}
	case RunRunning:
		return fmt.Errorf("%w: run completion status %q is not terminal", ErrInvalidAutomation, completion.Status)
	default:
		return fmt.Errorf("%w: run completion status %q is not terminal", ErrInvalidAutomation, completion.Status)
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		updated, err := queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
			RunStatus:      sql.NullString{String: string(completion.Status), Valid: true},
			RunFailureCode: encodeNullableString(completion.FailureCode),
			RunCompletedAt: sql.NullString{String: encodeAutomationTimestamp(repo.now()), Valid: true},
			ID:             string(completion.RunID),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("%w: run %s is not completable", ErrInvalidAutomation, completion.RunID)
		}
		return nil
	})
}

// GetHistoryEntry reads one retained Run or Skip scoped to its former
// Automation. A parent mismatch or unknown identity is [ErrHistoryNotFound].
func (repo *SQLiteRepository) GetHistoryEntry(
	ctx context.Context,
	automationID AutomationID,
	entryID string,
) (AutomationHistoryEntry, error) {
	var entry AutomationHistoryEntry
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetHistoryEntry(ctx, dbsqlc.GetHistoryEntryParams{
			AutomationID: string(automationID),
			ID:           entryID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrHistoryNotFound
		}
		if err != nil {
			return err
		}
		entry, err = historyEntry(ctx, queries, row)
		return err
	})
	if err != nil {
		return AutomationHistoryEntry{}, err
	}
	return entry, nil
}

// ListHistory pages retained Run and Skip summaries newest first for one
// Automation, including one that has been hard-deleted.
func (repo *SQLiteRepository) ListHistory(
	ctx context.Context,
	params ListHistoryParams,
) (AutomationPage[AutomationHistorySummary], error) {
	page := AutomationPage[AutomationHistorySummary]{Items: []AutomationHistorySummary{}}
	limit, err := automationPageLimit(params.Limit)
	if err != nil {
		return page, err
	}
	if (params.BeforeRecordedAt == nil) != (params.BeforeID == nil) {
		return page, fmt.Errorf("%w: history cursor requires a time and an ID", ErrInvalidAutomation)
	}
	var rows []dbsqlc.AutomationHistory
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		var queryErr error
		if params.BeforeRecordedAt == nil {
			rows, queryErr = queries.ListHistoryFirstPage(ctx, dbsqlc.ListHistoryFirstPageParams{
				AutomationID: string(params.AutomationID),
				Limit:        int64(limit + 1),
			})
		} else {
			recordedAt := encodeAutomationTimestamp(*params.BeforeRecordedAt)
			rows, queryErr = queries.ListHistoryAfter(ctx, dbsqlc.ListHistoryAfterParams{
				AutomationID: string(params.AutomationID),
				RecordedAt:   recordedAt,
				RecordedAt_2: recordedAt,
				ID:           *params.BeforeID,
				Limit:        int64(limit + 1),
			})
		}
		return queryErr
	})
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		summary, summaryErr := historySummary(row)
		if summaryErr != nil {
			return page, summaryErr
		}
		page.Items = append(page.Items, summary)
	}
	return page, nil
}

// InterruptActiveRuns marks every running Step and Run as interrupted with the
// supplied reason. It never replays, infers success, or touches a later Step.
func (repo *SQLiteRepository) InterruptActiveRuns(ctx context.Context, at time.Time, reason string) error {
	if at.IsZero() {
		return fmt.Errorf("%w: interruption time is required", ErrInvalidAutomation)
	}
	if reason == "" {
		return fmt.Errorf("%w: interruption reason is required", ErrInvalidAutomation)
	}
	completedAt := sql.NullString{String: encodeAutomationTimestamp(at), Valid: true}
	failureCode := sql.NullString{String: reason, Valid: true}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if _, err := queries.InterruptRunningRuns(ctx, dbsqlc.InterruptRunningRunsParams{
			RunFailureCode: failureCode,
			RunCompletedAt: completedAt,
		}); err != nil {
			return err
		}
		if _, err := queries.InterruptRunningSteps(ctx, dbsqlc.InterruptRunningStepsParams{
			FailureCode: failureCode,
			CompletedAt: completedAt,
		}); err != nil {
			return err
		}
		return nil
	})
}

// DeleteHistoryBefore removes at most limit terminal history records older than
// the cutoff in one transaction. Running Runs are never selected, and matched
// Fact receipts are deliberately retained.
func (repo *SQLiteRepository) DeleteHistoryBefore(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	if cutoff.IsZero() {
		return 0, fmt.Errorf("%w: retention cutoff is required", ErrInvalidAutomation)
	}
	if limit < 1 {
		return 0, fmt.Errorf("%w: retention batch must be positive", ErrInvalidAutomation)
	}
	var deleted int64
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		count, err := queries.DeleteHistoryBefore(ctx, dbsqlc.DeleteHistoryBeforeParams{
			RecordedAt: encodeAutomationTimestamp(cutoff),
			Limit:      int64(limit),
		})
		if err != nil {
			return err
		}
		deleted = count
		return nil
	})
	return deleted, err
}

// buildRun assembles one immutable Run from a current definition record. Every
// Step starts not_attempted, so no reserved identity ever leaks before the
// external call.
func (repo *SQLiteRepository) buildRun(
	record AutomationRecord,
	source RunSource,
	fact *DeviceFactSummary,
	matched []TriggerID,
	admittedAt time.Time,
) (AutomationRun, error) {
	runID, err := repo.newRunID()
	if err != nil {
		return AutomationRun{}, fmt.Errorf("allocate automation run ID: %w", err)
	}
	steps := make([]AutomationStepAttempt, len(record.Definition.Steps))
	for position, step := range record.Definition.Steps {
		steps[position] = AutomationStepAttempt{
			Position: position,
			StepID:   step.ID,
			Status:   StepNotAttempted,
		}
	}
	return AutomationRun{
		ID:                runID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Snapshot:          record.Definition,
		Source:            source,
		Fact:              fact,
		MatchedTriggerIDs: append([]TriggerID(nil), matched...),
		Status:            RunRunning,
		StartedAt:         admittedAt.UTC(),
		Steps:             steps,
	}, nil
}

func (repo *SQLiteRepository) persistRun(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run AutomationRun,
) error {
	if err := ValidateAutomationRun(run); err != nil {
		return err
	}
	snapshot, err := EncodeAutomationDefinition(run.Snapshot)
	if err != nil {
		return err
	}
	matched, err := encodeTriggerIDs(run.MatchedTriggerIDs)
	if err != nil {
		return err
	}
	fact := storedFactColumns(run.Fact)
	recordedAt := encodeAutomationTimestamp(run.StartedAt)
	if err = queries.CreateHistoryRun(ctx, dbsqlc.CreateHistoryRunParams{
		ID:                       string(run.ID),
		AutomationID:             string(run.AutomationID),
		AutomationName:           run.AutomationName,
		Revision:                 run.Revision,
		RecordedAt:               recordedAt,
		FactID:                   fact.id,
		FactFamily:               fact.family,
		FactEntityID:             fact.entityID,
		FactVariant:              fact.variant,
		FactCausationID:          fact.causationID,
		FactValueJson:            fact.valueJSON,
		FactEmittedAt:            fact.emittedAt,
		RunSnapshotJson:          sql.NullString{String: string(snapshot), Valid: true},
		RunSource:                sql.NullString{String: string(run.Source), Valid: true},
		RunStartedAt:             sql.NullString{String: recordedAt, Valid: true},
		RunMatchedTriggerIdsJson: sql.NullString{String: string(matched), Valid: true},
	}); err != nil {
		return err
	}
	for position, step := range run.Snapshot.Steps {
		if err = queries.CreateRunStep(ctx, dbsqlc.CreateRunStepParams{
			RunID:    string(run.ID),
			Position: int64(position),
			StepID:   string(step.ID),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (repo *SQLiteRepository) recordSkip(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record AutomationRecord,
	summary DeviceFactSummary,
	matched []TriggerID,
	reason AutomationSkipReason,
	skippedAt time.Time,
) (AdmissionSkip, error) {
	skipID, err := repo.newSkipID()
	if err != nil {
		return AdmissionSkip{}, fmt.Errorf("allocate automation skip ID: %w", err)
	}
	triggers, err := matchedTriggerSnapshots(record.Definition, matched)
	if err != nil {
		return AdmissionSkip{}, err
	}
	encodedTriggers, err := encodeMatchedTriggers(triggers)
	if err != nil {
		return AdmissionSkip{}, err
	}
	fact := storedFactColumns(&summary)
	if err = queries.CreateHistorySkip(ctx, dbsqlc.CreateHistorySkipParams{
		ID:                      string(skipID),
		AutomationID:            string(record.ID),
		AutomationName:          record.Definition.Name,
		Revision:                record.Revision,
		RecordedAt:              encodeAutomationTimestamp(skippedAt),
		FactID:                  fact.id,
		FactFamily:              fact.family,
		FactEntityID:            fact.entityID,
		FactVariant:             fact.variant,
		FactCausationID:         fact.causationID,
		FactValueJson:           fact.valueJSON,
		FactEmittedAt:           fact.emittedAt,
		SkipMatchedTriggersJson: sql.NullString{String: string(encodedTriggers), Valid: true},
		SkipReason:              sql.NullString{String: string(reason), Valid: true},
	}); err != nil {
		return AdmissionSkip{}, err
	}
	if err = repo.writeReceipt(
		ctx, queries, summary.FactID, record.ID, AutomationHistorySkip, string(skipID),
	); err != nil {
		return AdmissionSkip{}, err
	}
	return AdmissionSkip{
		SkipID:       skipID,
		AutomationID: record.ID,
		Revision:     record.Revision,
		Reason:       reason,
		FactID:       summary.FactID,
		Family:       summary.Family,
		Variant:      summary.Variant,
	}, nil
}

func (repo *SQLiteRepository) writeReceipt(
	ctx context.Context,
	queries *dbsqlc.Queries,
	factID devices.DeviceFactID,
	automationID AutomationID,
	kind AutomationHistoryKind,
	historyID string,
) error {
	return queries.CreateFactReceipt(ctx, dbsqlc.CreateFactReceiptParams{
		FactID:       string(factID),
		AutomationID: string(automationID),
		OutcomeKind:  string(kind),
		HistoryID:    historyID,
	})
}

// matchedTriggerSnapshots copies the matching Triggers in definition order so a
// retained Skip stays explainable after the definition is edited or deleted.
func matchedTriggerSnapshots(
	definition AutomationDefinition,
	matched []TriggerID,
) ([]AutomationTrigger, error) {
	byID := make(map[TriggerID]AutomationTrigger, len(definition.Triggers))
	for _, trigger := range definition.Triggers {
		byID[trigger.ID] = trigger
	}
	snapshots := make([]AutomationTrigger, 0, len(matched))
	for _, id := range matched {
		trigger, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: matched trigger %q is not in the definition", ErrInvalidAutomation, id)
		}
		snapshots = append(snapshots, trigger)
	}
	return snapshots, nil
}

// historyEntry reads one full Run or Skip, including the ordered Steps of a Run.
func historyEntry(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (AutomationHistoryEntry, error) {
	switch AutomationHistoryKind(row.Kind) {
	case AutomationHistoryRun:
		run, err := runFromRow(ctx, queries, row)
		if err != nil {
			return AutomationHistoryEntry{}, err
		}
		return AutomationHistoryEntry{Kind: AutomationHistoryRun, Run: &run}, nil
	case AutomationHistorySkip:
		skip, err := skipFromRow(row)
		if err != nil {
			return AutomationHistoryEntry{}, err
		}
		return AutomationHistoryEntry{Kind: AutomationHistorySkip, Skip: &skip}, nil
	default:
		return AutomationHistoryEntry{}, fmt.Errorf(
			"%w: stored history %q has unknown kind %q", ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
}

func historySummary(row dbsqlc.AutomationHistory) (AutomationHistorySummary, error) {
	automationID, err := ParseAutomationID(row.AutomationID)
	if err != nil {
		return AutomationHistorySummary{}, fmt.Errorf("stored history %q: %w", row.ID, err)
	}
	if row.Revision < 1 {
		return AutomationHistorySummary{}, fmt.Errorf(
			"%w: stored history %q revision %d", ErrInvalidAutomation, row.ID, row.Revision,
		)
	}
	recordedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return AutomationHistorySummary{}, fmt.Errorf("stored history %q recorded_at: %w", row.ID, err)
	}
	summary := AutomationHistorySummary{
		ID:             row.ID,
		Kind:           AutomationHistoryKind(row.Kind),
		AutomationID:   automationID,
		AutomationName: row.AutomationName,
		Revision:       row.Revision,
		RecordedAt:     recordedAt,
	}
	switch summary.Kind {
	case AutomationHistoryRun:
		if !row.RunStatus.Valid {
			return summary, fmt.Errorf("%w: stored Run %q has no status", ErrInvalidAutomation, row.ID)
		}
		summary.Status = RunStatus(row.RunStatus.String)
		fact, factErr := factSummaryFromRow(row)
		if factErr != nil {
			return summary, factErr
		}
		summary.Fact = fact
	case AutomationHistorySkip:
		if !row.SkipReason.Valid {
			return summary, fmt.Errorf("%w: stored Skip %q has no reason", ErrInvalidAutomation, row.ID)
		}
		summary.Reason = AutomationSkipReason(row.SkipReason.String)
		fact, factErr := factSummaryFromRow(row)
		if factErr != nil {
			return summary, factErr
		}
		if fact == nil {
			return summary, fmt.Errorf("%w: stored Skip %q has no Fact summary", ErrInvalidAutomation, row.ID)
		}
		summary.Fact = fact
	default:
		return summary, fmt.Errorf("%w: stored history %q has unknown kind %q", ErrInvalidAutomation, row.ID, row.Kind)
	}
	return summary, nil
}

func runFromRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (AutomationRun, error) {
	if !row.RunSnapshotJson.Valid || !row.RunSource.Valid || !row.RunStatus.Valid || !row.RunStartedAt.Valid ||
		!row.RunMatchedTriggerIdsJson.Valid {
		return AutomationRun{}, fmt.Errorf("%w: stored Run %q is incomplete", ErrInvalidAutomation, row.ID)
	}
	runID, err := ParseAutomationRunID(row.ID)
	if err != nil {
		return AutomationRun{}, err
	}
	automationID, err := ParseAutomationID(row.AutomationID)
	if err != nil {
		return AutomationRun{}, fmt.Errorf("stored Run %q: %w", row.ID, err)
	}
	snapshot, err := DecodeAutomationDefinition(json.RawMessage(row.RunSnapshotJson.String))
	if err != nil {
		return AutomationRun{}, fmt.Errorf("stored Run %q snapshot: %w", row.ID, err)
	}
	matched, err := decodeTriggerIDs(json.RawMessage(row.RunMatchedTriggerIdsJson.String))
	if err != nil {
		return AutomationRun{}, fmt.Errorf("stored Run %q matched triggers: %w", row.ID, err)
	}
	startedAt, err := decodeAutomationTimestamp(row.RunStartedAt.String)
	if err != nil {
		return AutomationRun{}, fmt.Errorf("stored Run %q started_at: %w", row.ID, err)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.RunCompletedAt)
	if err != nil {
		return AutomationRun{}, fmt.Errorf("stored Run %q completed_at: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return AutomationRun{}, err
	}
	steps, err := runSteps(ctx, queries, row.ID)
	if err != nil {
		return AutomationRun{}, err
	}
	run := AutomationRun{
		ID:                runID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Snapshot:          snapshot,
		Source:            RunSource(row.RunSource.String),
		Fact:              fact,
		MatchedTriggerIDs: matched,
		Status:            RunStatus(row.RunStatus.String),
		FailureCode:       stringPointer(row.RunFailureCode),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		Steps:             steps,
	}
	if err = ValidateAutomationRun(run); err != nil {
		return AutomationRun{}, err
	}
	return run, nil
}

func skipFromRow(row dbsqlc.AutomationHistory) (AutomationSkip, error) {
	if !row.SkipMatchedTriggersJson.Valid || !row.SkipReason.Valid {
		return AutomationSkip{}, fmt.Errorf("%w: stored Skip %q is incomplete", ErrInvalidAutomation, row.ID)
	}
	skipID, err := ParseAutomationSkipID(row.ID)
	if err != nil {
		return AutomationSkip{}, err
	}
	automationID, err := ParseAutomationID(row.AutomationID)
	if err != nil {
		return AutomationSkip{}, fmt.Errorf("stored Skip %q: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return AutomationSkip{}, err
	}
	if fact == nil {
		return AutomationSkip{}, fmt.Errorf("%w: stored Skip %q has no Fact summary", ErrInvalidAutomation, row.ID)
	}
	triggers, err := decodeMatchedTriggers(json.RawMessage(row.SkipMatchedTriggersJson.String))
	if err != nil {
		return AutomationSkip{}, fmt.Errorf("stored Skip %q matched triggers: %w", row.ID, err)
	}
	skippedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return AutomationSkip{}, fmt.Errorf("stored Skip %q recorded_at: %w", row.ID, err)
	}
	skip := AutomationSkip{
		ID:              skipID,
		AutomationID:    automationID,
		AutomationName:  row.AutomationName,
		Revision:        row.Revision,
		Fact:            *fact,
		MatchedTriggers: triggers,
		Reason:          AutomationSkipReason(row.SkipReason.String),
		SkippedAt:       skippedAt,
	}
	if err = ValidateAutomationSkip(skip); err != nil {
		return AutomationSkip{}, err
	}
	return skip, nil
}

func runSteps(ctx context.Context, queries *dbsqlc.Queries, runID string) ([]AutomationStepAttempt, error) {
	rows, err := queries.ListRunSteps(ctx, dbsqlc.ListRunStepsParams{RunID: runID})
	if err != nil {
		return nil, err
	}
	steps := make([]AutomationStepAttempt, 0, len(rows))
	for _, row := range rows {
		step, stepErr := stepAttemptFromRow(row)
		if stepErr != nil {
			return nil, stepErr
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func stepAttemptFromRow(row dbsqlc.AutomationRunStep) (AutomationStepAttempt, error) {
	stepID, err := ParseStepID(row.StepID)
	if err != nil {
		return AutomationStepAttempt{}, fmt.Errorf("stored step %s/%d: %w", row.RunID, row.Position, err)
	}
	startedAt, err := parseNullableAutomationTimestamp(row.StartedAt)
	if err != nil {
		return AutomationStepAttempt{}, fmt.Errorf("stored step %s/%d started_at: %w", row.RunID, row.Position, err)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return AutomationStepAttempt{}, fmt.Errorf("stored step %s/%d completed_at: %w", row.RunID, row.Position, err)
	}
	step := AutomationStepAttempt{
		Position:              int(row.Position),
		StepID:                stepID,
		Status:                StepStatus(row.Status),
		ReservedCommandID:     commandIDPointer(row.ReservedCommandID),
		ReservedCorrelationID: correlationIDPointer(row.ReservedCorrelationID),
		VerifiedCommandID:     commandIDPointer(row.VerifiedCommandID),
		FailureCode:           stringPointer(row.FailureCode),
		StartedAt:             startedAt,
		CompletedAt:           completedAt,
	}
	return step, nil
}

// factSummaryFromRow decodes the copied Fact summary that explains one outcome.
// It returns nil when the row carries no Fact evidence, which only a manual Run
// may do.
func factSummaryFromRow(row dbsqlc.AutomationHistory) (*DeviceFactSummary, error) {
	if !row.FactID.Valid {
		return nil, nil //nolint:nilnil // An absent Fact summary is the manual-Run case.
	}
	if !row.FactFamily.Valid || !row.FactEntityID.Valid || !row.FactVariant.Valid ||
		!row.FactCausationID.Valid || !row.FactEmittedAt.Valid {
		return nil, fmt.Errorf("%w: stored history %q has a partial Fact summary", ErrInvalidAutomation, row.ID)
	}
	emittedAt, err := decodeAutomationTimestamp(row.FactEmittedAt.String)
	if err != nil {
		return nil, fmt.Errorf("stored history %q fact_emitted_at: %w", row.ID, err)
	}
	summary := &DeviceFactSummary{
		FactID:      devices.DeviceFactID(row.FactID.String),
		Family:      DeviceFactFamily(row.FactFamily.String),
		EntityID:    devices.EntityID(row.FactEntityID.String),
		Variant:     row.FactVariant.String,
		CausationID: row.FactCausationID.String,
		EmittedAt:   emittedAt,
	}
	if row.FactValueJson.Valid {
		summary.ObservationValue = devices.Value(row.FactValueJson.String)
	}
	if err = ValidateDeviceFactSummary(*summary); err != nil {
		return nil, err
	}
	return summary, nil
}

type storedFact struct {
	id          sql.NullString
	family      sql.NullString
	entityID    sql.NullString
	variant     sql.NullString
	causationID sql.NullString
	valueJSON   sql.NullString
	emittedAt   sql.NullString
}

func storedFactColumns(summary *DeviceFactSummary) storedFact {
	if summary == nil {
		return storedFact{}
	}
	stored := storedFact{
		id:          sql.NullString{String: string(summary.FactID), Valid: true},
		family:      sql.NullString{String: string(summary.Family), Valid: true},
		entityID:    sql.NullString{String: string(summary.EntityID), Valid: true},
		variant:     sql.NullString{String: summary.Variant, Valid: true},
		causationID: sql.NullString{String: summary.CausationID, Valid: true},
		emittedAt:   sql.NullString{String: encodeAutomationTimestamp(summary.EmittedAt), Valid: true},
	}
	if summary.ObservationValue != nil {
		stored.valueJSON = sql.NullString{String: string(summary.ObservationValue), Valid: true}
	}
	return stored
}

func encodeTriggerIDs(ids []TriggerID) (json.RawMessage, error) {
	if ids == nil {
		ids = []TriggerID{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("%w: matched trigger IDs cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

func decodeTriggerIDs(raw json.RawMessage) ([]TriggerID, error) {
	var ids []TriggerID
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := ParseTriggerID(string(id)); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func encodeMatchedTriggers(triggers []AutomationTrigger) (json.RawMessage, error) {
	encoded := make([]automationTriggerJSON, 0, len(triggers))
	for _, trigger := range triggers {
		encoded = append(encoded, encodeAutomationTrigger(trigger))
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: matched triggers cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

func decodeMatchedTriggers(raw json.RawMessage) ([]AutomationTrigger, error) {
	var encoded []automationTriggerJSON
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	triggers := make([]AutomationTrigger, 0, len(encoded))
	for _, item := range encoded {
		trigger, err := normalizeAutomationTriggerValue(automationTriggerFromJSON(item))
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

func validateStepCompletion(completion StepCompletion) error {
	switch completion.Status {
	case StepNotAttempted, StepRunning:
		return fmt.Errorf("%w: step completion status %q is not terminal", ErrInvalidAutomation, completion.Status)
	case StepSatisfied, StepDispatched:
		if completion.VerifiedCommandID == nil || completion.FailureCode != nil {
			return fmt.Errorf(
				"%w: successful step requires a verified Command and no failure code",
				ErrInvalidAutomation,
			)
		}
	case StepFailed, StepInterrupted:
		if completion.FailureCode == nil {
			return fmt.Errorf("%w: failing step requires a failure code", ErrInvalidAutomation)
		}
	default:
		return fmt.Errorf("%w: unknown step completion status %q", ErrInvalidAutomation, completion.Status)
	}
	if completion.VerifiedCommandID != nil {
		if _, err := devices.ParseCommandID(string(*completion.VerifiedCommandID)); err != nil {
			return fmt.Errorf("%w: verified command ID: %w", ErrInvalidAutomation, err)
		}
	}
	return nil
}

func encodeNullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func commandIDString(value *devices.CommandID) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func commandIDPointer(value sql.NullString) *devices.CommandID {
	if !value.Valid {
		return nil
	}
	id := devices.CommandID(value.String)
	return &id
}

func correlationIDPointer(value sql.NullString) *devices.CorrelationID {
	if !value.Valid {
		return nil
	}
	id := devices.CorrelationID(value.String)
	return &id
}

func parseNullableAutomationTimestamp(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil //nolint:nilnil // An absent optional timestamp is not an error.
	}
	parsed, err := decodeAutomationTimestamp(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
