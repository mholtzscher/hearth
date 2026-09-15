package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AdmitDeviceFact atomically commits matching enabled Automations' receipts,
// Runs, Skips, and initial Steps. The caller starts workers only after commit.
// Matching uses the definitions this transaction loaded, never a preliminary
// Service read.
func (repo *AutomationRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	admittedAt time.Time,
) (automations.AdmissionResult, error) {
	if err := automations.ValidateDeviceFact(fact); err != nil {
		return automations.AdmissionResult{}, err
	}
	if admittedAt.IsZero() {
		return automations.AdmissionResult{}, fmt.Errorf(
			"%w: admission time is required", automations.ErrInvalidDeviceFact,
		)
	}
	summary := automations.NewDeviceFactSummary(fact)
	var result automations.AdmissionResult
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
		return automations.AdmissionResult{}, err
	}
	return result, nil
}

// AdmitManualRun starts one Run from the current definition snapshot even when
// the Automation is disabled. A running Run for the same Automation returns
// [automations.ErrAutomationBusy]; a missing definition returns
// [automations.ErrAutomationNotFound].
func (repo *AutomationRepository) AdmitManualRun(
	ctx context.Context,
	id automations.AutomationID,
	admittedAt time.Time,
) (automations.AutomationRun, error) {
	if _, err := automations.ParseAutomationID(string(id)); err != nil {
		return automations.AutomationRun{}, err
	}
	if admittedAt.IsZero() {
		return automations.AutomationRun{}, fmt.Errorf(
			"%w: admission time is required", automations.ErrInvalidAutomation,
		)
	}
	var run automations.AutomationRun
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(id)})
		if errors.Is(err, sql.ErrNoRows) {
			return automations.ErrAutomationNotFound
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
			return automations.ErrAutomationBusy
		}
		runID, err := repo.newRunID()
		if err != nil {
			return fmt.Errorf("allocate automation run ID: %w", err)
		}
		run = automations.NewAutomationRunSnapshot(record, runID, automations.RunSourceManual, nil, nil, admittedAt)
		return repo.persistRun(ctx, queries, run)
	})
	if err != nil {
		return automations.AutomationRun{}, err
	}
	return run, nil
}

// admitAutomationRow evaluates one current definition and folds its single
// outcome into the admission result.
func (repo *AutomationRepository) admitAutomationRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.Automation,
	fact automations.DeviceFact,
	summary automations.DeviceFactSummary,
	admittedAt time.Time,
	result *automations.AdmissionResult,
) error {
	record, err := automationRecord(row)
	if err != nil {
		return err
	}
	if !record.Definition.Enabled {
		return nil
	}
	matched, err := automations.MatchAutomationTriggers(fact, record.Definition)
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

// admitMatchedDeviceFact records a Run or Skip, ignoring duplicate receipts.
// Freshness precedes the busy check, so old matching Facts record stale_fact.
func (repo *AutomationRepository) admitMatchedDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.AutomationRecord,
	summary automations.DeviceFactSummary,
	matched []automations.TriggerID,
	admittedAt time.Time,
) (*automations.AutomationRun, *automations.AdmissionSkip, error) {
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
	if admittedAt.Sub(summary.EmittedAt) > automations.AutomationFactMaximumAge {
		skip, recordErr := repo.recordSkip(
			ctx, queries, record, summary, matched, automations.AutomationSkipStaleFact, admittedAt,
		)
		if recordErr != nil {
			return nil, nil, recordErr
		}
		return nil, &skip, nil
	}
	if running > 0 {
		skip, recordErr := repo.recordSkip(
			ctx, queries, record, summary, matched, automations.AutomationSkipBusy, admittedAt,
		)
		if recordErr != nil {
			return nil, nil, recordErr
		}
		return nil, &skip, nil
	}
	runID, err := repo.newRunID()
	if err != nil {
		return nil, nil, fmt.Errorf("allocate automation run ID: %w", err)
	}
	run := automations.NewAutomationRunSnapshot(
		record, runID, automations.RunSourceDeviceFact, &summary, matched, admittedAt,
	)
	if err = repo.persistRun(ctx, queries, run); err != nil {
		return nil, nil, err
	}
	if err = repo.writeReceipt(
		ctx, queries, summary.FactID, record.ID, automations.AutomationHistoryRun, string(run.ID),
	); err != nil {
		return nil, nil, err
	}
	return &run, nil, nil
}

// persistRun writes one Run snapshot and its initial not_attempted Steps. The
// caller owns the enclosing transaction.
func (repo *AutomationRepository) persistRun(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.AutomationRun,
) error {
	if err := automations.ValidateAutomationRun(run); err != nil {
		return err
	}
	snapshot, err := automations.EncodeAutomationDefinition(run.Snapshot)
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

// recordSkip records one matching Fact's busy or stale_fact outcome with its
// Trigger snapshots and deduplication receipt. The caller owns the transaction.
func (repo *AutomationRepository) recordSkip(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.AutomationRecord,
	summary automations.DeviceFactSummary,
	matched []automations.TriggerID,
	reason automations.AutomationSkipReason,
	skippedAt time.Time,
) (automations.AdmissionSkip, error) {
	skipID, err := repo.newSkipID()
	if err != nil {
		return automations.AdmissionSkip{}, fmt.Errorf("allocate automation skip ID: %w", err)
	}
	triggers, err := automations.MatchedTriggerSnapshots(record.Definition, matched)
	if err != nil {
		return automations.AdmissionSkip{}, err
	}
	encodedTriggers, err := automations.EncodeMatchedTriggers(triggers)
	if err != nil {
		return automations.AdmissionSkip{}, err
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
		return automations.AdmissionSkip{}, err
	}
	if err = repo.writeReceipt(
		ctx, queries, summary.FactID, record.ID, automations.AutomationHistorySkip, string(skipID),
	); err != nil {
		return automations.AdmissionSkip{}, err
	}
	return automations.AdmissionSkip{
		SkipID:       skipID,
		AutomationID: record.ID,
		Revision:     record.Revision,
		Reason:       reason,
		FactID:       summary.FactID,
		Family:       summary.Family,
		Variant:      summary.Variant,
	}, nil
}

// writeReceipt records one matched-Fact outcome so a redelivered Fact never
// creates a second Run or Skip for the same Automation.
func (repo *AutomationRepository) writeReceipt(
	ctx context.Context,
	queries *dbsqlc.Queries,
	factID devices.DeviceFactID,
	automationID automations.AutomationID,
	kind automations.AutomationHistoryKind,
	historyID string,
) error {
	return queries.CreateFactReceipt(ctx, dbsqlc.CreateFactReceiptParams{
		FactID:       string(factID),
		AutomationID: string(automationID),
		OutcomeKind:  string(kind),
		HistoryID:    historyID,
	})
}
