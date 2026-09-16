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

// plannedOutcomeKind is the decided, still-unwritten outcome of one matching
// Automation. Nothing here has allocated an identity or written a row.
type plannedOutcomeKind int

const (
	plannedOutcomeRun plannedOutcomeKind = iota
	plannedOutcomeSkip
	plannedOutcomeDuplicate
)

// plannedAutomation carries one matching Automation's decided, still-unwritten
// outcome. Condition evaluation happens during planning after coverage is
// verified, so any definition-edit race returns before the transaction writes.
type plannedAutomation struct {
	record   automations.Record
	matched  []automations.TriggerID
	kind     plannedOutcomeKind
	reason   automations.SkipReason
	decision automations.ConditionDecision
}

// AdmitDeviceFact atomically commits matching enabled Automations' receipts,
// Runs, Skips, and initial Steps. The caller starts workers only after commit.
// Matching uses the definitions this transaction loaded, never a preliminary
// Service read.
//
// The transaction plans every matching outcome without writes or identity
// allocation, applies duplicate/stale/busy precedence before evaluating
// Conditions, and returns [automations.ConditionSnapshotRequiredError] when a
// definition changed after the Service pre-read and needs an uncovered Entity.
// That rare coverage-error pass commits nothing, including otherwise-decided
// stale, busy, and unconditioned siblings.
func (repo *AutomationRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
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
		plans, err := repo.planDeviceFact(ctx, queries, fact, summary, snapshot, admittedAt)
		if err != nil {
			return err
		}
		return repo.commitDeviceFact(ctx, queries, plans, summary, admittedAt, &result)
	})
	if err != nil {
		return automations.AdmissionResult{}, err
	}
	return result, nil
}

// AdmitManualRun commits exactly one manual Run or manual Condition Skip from
// the current definition snapshot, even when the Automation is disabled. Manual
// invocation ignores definition enablement and has no Trigger or Fact.
//
// A running Run for the same Automation returns [automations.ErrAutomationBusy]
// without writing a Skip; a missing definition returns
// [automations.ErrAutomationNotFound]. An explicit bypass never reads State or
// evaluates a Condition. The result is returned only after commit, so the
// Service can turn a committed Skip into its typed blocked error.
func (repo *AutomationRepository) AdmitManualRun(
	ctx context.Context,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
) (automations.ManualAdmissionResult, error) {
	if _, err := automations.ParseAutomationID(string(input.AutomationID)); err != nil {
		return automations.ManualAdmissionResult{}, err
	}
	if admittedAt.IsZero() {
		return automations.ManualAdmissionResult{}, fmt.Errorf(
			"%w: admission time is required", automations.ErrInvalidAutomation,
		)
	}
	var result automations.ManualAdmissionResult
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		return repo.admitManualRun(ctx, queries, input, snapshot, admittedAt, &result)
	})
	if err != nil {
		return automations.ManualAdmissionResult{}, err
	}
	return result, nil
}

// admitManualRun decides and commits one manual admission inside the caller's
// transaction. It writes either one Run or one manual Condition Skip.
func (repo *AutomationRepository) admitManualRun(
	ctx context.Context,
	queries *dbsqlc.Queries,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
	result *automations.ManualAdmissionResult,
) error {
	row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: string(input.AutomationID)})
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
	decision, blocked, err := manualConditionDecision(
		record.Definition.Conditions, input.BypassConditions, snapshot, admittedAt,
	)
	if err != nil {
		return err
	}
	if blocked != "" {
		skip, persistErr := repo.persistManualSkip(ctx, queries, record, blocked, decision, admittedAt)
		if persistErr != nil {
			return persistErr
		}
		result.Skip = &skip
		return nil
	}
	runID, err := repo.newRunID()
	if err != nil {
		return fmt.Errorf("allocate automation run ID: %w", err)
	}
	run := automations.NewRunSnapshot(
		record, runID, automations.RunSourceManual, nil, nil, decision, admittedAt,
	)
	if err = repo.persistRun(ctx, queries, run); err != nil {
		return err
	}
	result.Run = &run
	return nil
}

// planDeviceFact loads current definitions and decides every matching
// Automation's outcome without writing history, allocating identities, or
// registering workers. A definition-edit race with the Service's pre-read
// returns a coverage error before any planned outcome is committed.
func (repo *AutomationRepository) planDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	fact automations.DeviceFact,
	summary automations.DeviceFactSummary,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
) ([]plannedAutomation, error) {
	rows, err := queries.ListAllAutomations(ctx)
	if err != nil {
		return nil, err
	}
	plans := make([]plannedAutomation, 0, len(rows))
	for _, row := range rows {
		plan, planErr := repo.planAutomationOutcome(ctx, queries, row, fact, summary, snapshot, admittedAt)
		if planErr != nil {
			return nil, planErr
		}
		if plan != nil {
			plans = append(plans, *plan)
		}
	}
	return plans, nil
}

// planAutomationOutcome decides one current definition's outcome without
// writing history, allocating identities, or registering workers. It returns nil
// for a disabled or unmatched Automation, which records nothing at all.
func (repo *AutomationRepository) planAutomationOutcome(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.Automation,
	fact automations.DeviceFact,
	summary automations.DeviceFactSummary,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
) (*plannedAutomation, error) {
	record, err := automationRecord(row)
	if err != nil {
		return nil, err
	}
	if !record.Definition.Enabled {
		return nil, nil //nolint:nilnil // A non-matching Automation plans no outcome.
	}
	matched, err := automations.MatchTriggers(fact, record.Definition)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		return nil, nil //nolint:nilnil // A non-matching Automation plans no outcome.
	}
	receipts, err := queries.CountFactReceipts(ctx, dbsqlc.CountFactReceiptsParams{
		FactID:       string(summary.FactID),
		AutomationID: string(record.ID),
	})
	if err != nil {
		return nil, err
	}
	if receipts > 0 {
		return &plannedAutomation{record: record, matched: matched, kind: plannedOutcomeDuplicate}, nil
	}
	// Freshness precedes the busy check, so an old matching Fact records
	// stale_fact even while another Run is active.
	if admittedAt.Sub(summary.EmittedAt) > automations.FactMaximumAge {
		return &plannedAutomation{
			record:   record,
			matched:  matched,
			kind:     plannedOutcomeSkip,
			reason:   automations.SkipStaleFact,
			decision: notEvaluatedDecision(record.Definition.Conditions),
		}, nil
	}
	running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{
		AutomationID: string(record.ID),
	})
	if err != nil {
		return nil, err
	}
	if running > 0 {
		return &plannedAutomation{
			record:   record,
			matched:  matched,
			kind:     plannedOutcomeSkip,
			reason:   automations.SkipBusy,
			decision: notEvaluatedDecision(record.Definition.Conditions),
		}, nil
	}
	conditions := record.Definition.Conditions
	if conditions == nil {
		return &plannedAutomation{
			record:  record,
			matched: matched,
			kind:    plannedOutcomeRun,
			decision: automations.ConditionDecision{
				Mode: automations.ConditionDecisionNotConfigured,
			},
		}, nil
	}
	return planConditionalOutcome(record, matched, conditions, snapshot, admittedAt)
}

// planConditionalOutcome plans the admitted Run or Condition Skip that one
// configured Condition tree implies.
func planConditionalOutcome(
	record automations.Record,
	matched []automations.TriggerID,
	conditions *automations.Condition,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
) (*plannedAutomation, error) {
	decision, reason, err := automations.DecideConditions(conditions, snapshot, admittedAt)
	if err != nil {
		return nil, err
	}
	plan := &plannedAutomation{
		record:   record,
		matched:  matched,
		decision: decision,
	}
	if reason == "" {
		plan.kind = plannedOutcomeRun
	} else {
		plan.kind = plannedOutcomeSkip
		plan.reason = reason
	}
	return plan, nil
}

// commitDeviceFact writes every planned outcome in Automation ID order. The
// caller owns the enclosing transaction, so one failure rolls back all writes.
func (repo *AutomationRepository) commitDeviceFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	plans []plannedAutomation,
	summary automations.DeviceFactSummary,
	admittedAt time.Time,
	result *automations.AdmissionResult,
) error {
	for _, plan := range plans {
		result.Outcome.MatchedAutomations++
		switch plan.kind {
		case plannedOutcomeDuplicate:
			result.Outcome.DuplicateOutcomes++
		case plannedOutcomeRun:
			runID, err := repo.newRunID()
			if err != nil {
				return fmt.Errorf("allocate automation run ID: %w", err)
			}
			run := automations.NewRunSnapshot(
				plan.record,
				runID,
				automations.RunSourceDeviceFact,
				&summary,
				plan.matched,
				plan.decision,
				admittedAt,
			)
			if err = repo.persistRun(ctx, queries, run); err != nil {
				return err
			}
			if err = repo.writeReceipt(
				ctx, queries, summary.FactID, plan.record.ID,
				automations.HistoryRun, string(run.ID),
			); err != nil {
				return err
			}
			result.Outcome.StartedRuns++
			result.StartedRuns = append(result.StartedRuns, run)
		case plannedOutcomeSkip:
			skip, err := repo.recordSkip(ctx, queries, plan, summary, admittedAt)
			if err != nil {
				return err
			}
			result.Outcome.RecordedSkips++
			result.Skips = append(result.Skips, skip)
		}
	}
	return nil
}

// persistRun writes one Run snapshot and its initial not_attempted Steps. The
// caller owns the enclosing transaction.
func (repo *AutomationRepository) persistRun(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.Run,
) error {
	if err := automations.ValidateRun(run); err != nil {
		return err
	}
	snapshot, err := automations.EncodeDefinition(run.Snapshot)
	if err != nil {
		return err
	}
	matched, err := encodeTriggerIDs(run.MatchedTriggerIDs)
	if err != nil {
		return err
	}
	decision, err := automations.EncodeConditionDecision(run.ConditionDecision)
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
		ConditionDecisionJson:    string(decision),
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

// recordSkip records one matching Fact's non-Run outcome with its admission
// provenance, Trigger snapshots, Condition decision, and deduplication receipt.
// It validates the full Skip before writing, so a fabricated provenance never
// reaches storage. The caller owns the transaction.
func (repo *AutomationRepository) recordSkip(
	ctx context.Context,
	queries *dbsqlc.Queries,
	plan plannedAutomation,
	summary automations.DeviceFactSummary,
	skippedAt time.Time,
) (automations.AdmissionSkip, error) {
	skipID, err := repo.newSkipID()
	if err != nil {
		return automations.AdmissionSkip{}, fmt.Errorf("allocate automation skip ID: %w", err)
	}
	triggers, err := automations.MatchedTriggerSnapshots(plan.record.Definition, plan.matched)
	if err != nil {
		return automations.AdmissionSkip{}, err
	}
	skip := automations.Skip{
		ID:                skipID,
		AutomationID:      plan.record.ID,
		AutomationName:    plan.record.Definition.Name,
		Revision:          plan.record.Revision,
		Source:            automations.RunSourceDeviceFact,
		Fact:              &summary,
		MatchedTriggers:   triggers,
		Reason:            plan.reason,
		ConditionDecision: plan.decision,
		SkippedAt:         skippedAt.UTC(),
	}
	return repo.persistDeviceFactSkip(ctx, queries, skip)
}

// persistDeviceFactSkip writes one validated device-fact Skip and its
// deduplication receipt.
func (repo *AutomationRepository) persistDeviceFactSkip(
	ctx context.Context,
	queries *dbsqlc.Queries,
	skip automations.Skip,
) (automations.AdmissionSkip, error) {
	if err := automations.ValidateSkip(skip); err != nil {
		return automations.AdmissionSkip{}, err
	}
	encodedTriggers, err := automations.EncodeMatchedTriggers(skip.MatchedTriggers)
	if err != nil {
		return automations.AdmissionSkip{}, err
	}
	decision, err := automations.EncodeConditionDecision(skip.ConditionDecision)
	if err != nil {
		return automations.AdmissionSkip{}, err
	}
	fact := storedFactColumns(skip.Fact)
	if err = queries.CreateHistorySkip(ctx, dbsqlc.CreateHistorySkipParams{
		ID:                      string(skip.ID),
		AutomationID:            string(skip.AutomationID),
		AutomationName:          skip.AutomationName,
		Revision:                skip.Revision,
		RecordedAt:              encodeAutomationTimestamp(skip.SkippedAt),
		FactID:                  fact.id,
		FactFamily:              fact.family,
		FactEntityID:            fact.entityID,
		FactVariant:             fact.variant,
		FactCausationID:         fact.causationID,
		FactValueJson:           fact.valueJSON,
		FactEmittedAt:           fact.emittedAt,
		SkipMatchedTriggersJson: sql.NullString{String: string(encodedTriggers), Valid: true},
		SkipReason:              sql.NullString{String: string(skip.Reason), Valid: true},
		SkipSource:              sql.NullString{String: string(skip.Source), Valid: true},
		ConditionDecisionJson:   string(decision),
	}); err != nil {
		return automations.AdmissionSkip{}, err
	}
	if err = repo.writeReceipt(
		ctx, queries, skip.Fact.FactID, skip.AutomationID, automations.HistorySkip, string(skip.ID),
	); err != nil {
		return automations.AdmissionSkip{}, err
	}
	return automations.AdmissionSkip{
		SkipID:       skip.ID,
		AutomationID: skip.AutomationID,
		Revision:     skip.Revision,
		Source:       skip.Source,
		Reason:       skip.Reason,
		FactID:       &skip.Fact.FactID,
		Family:       skip.Fact.Family,
		Variant:      skip.Fact.Variant,
	}, nil
}

// persistManualSkip writes one validated manual Condition Skip. It has no Fact,
// no Trigger snapshots, no Steps, and no receipt, so repeated blocked manual
// requests stay distinct.
func (repo *AutomationRepository) persistManualSkip(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.Record,
	reason automations.SkipReason,
	decision automations.ConditionDecision,
	skippedAt time.Time,
) (automations.Skip, error) {
	skipID, err := repo.newSkipID()
	if err != nil {
		return automations.Skip{}, fmt.Errorf("allocate automation skip ID: %w", err)
	}
	skip := automations.Skip{
		ID:                skipID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Source:            automations.RunSourceManual,
		MatchedTriggers:   []automations.Trigger{},
		Reason:            reason,
		ConditionDecision: decision,
		SkippedAt:         skippedAt.UTC(),
	}
	if err = automations.ValidateSkip(skip); err != nil {
		return automations.Skip{}, err
	}
	encodedTriggers, err := automations.EncodeMatchedTriggers(skip.MatchedTriggers)
	if err != nil {
		return automations.Skip{}, err
	}
	encodedDecision, err := automations.EncodeConditionDecision(decision)
	if err != nil {
		return automations.Skip{}, err
	}
	if err = queries.CreateHistorySkip(ctx, dbsqlc.CreateHistorySkipParams{
		ID:                      string(skip.ID),
		AutomationID:            string(skip.AutomationID),
		AutomationName:          skip.AutomationName,
		Revision:                skip.Revision,
		RecordedAt:              encodeAutomationTimestamp(skip.SkippedAt),
		SkipMatchedTriggersJson: sql.NullString{String: string(encodedTriggers), Valid: true},
		SkipReason:              sql.NullString{String: string(skip.Reason), Valid: true},
		SkipSource:              sql.NullString{String: string(skip.Source), Valid: true},
		ConditionDecisionJson:   string(encodedDecision),
	}); err != nil {
		return automations.Skip{}, err
	}
	return skip, nil
}

// manualConditionDecision decides one manual admission's Condition decision. It
// returns a non-empty blocked reason only when Conditions evaluated false or
// unknown. An explicit bypass never reads State or evaluates a Condition, and a
// definition without Conditions records not_configured rather than a fabricated
// evaluation.
func manualConditionDecision(
	conditions *automations.Condition,
	bypass bool,
	snapshot devices.EntityStateSnapshot,
	admittedAt time.Time,
) (automations.ConditionDecision, automations.SkipReason, error) {
	if bypass {
		if conditions == nil {
			return automations.ConditionDecision{
				Mode:            automations.ConditionDecisionNotConfigured,
				BypassRequested: true,
			}, "", nil
		}
		return automations.ConditionDecision{
			Mode:            automations.ConditionDecisionBypassed,
			BypassRequested: true,
			Snapshot:        conditions,
		}, "", nil
	}
	if conditions == nil {
		return automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotConfigured,
		}, "", nil
	}
	return automations.DecideConditions(conditions, snapshot, admittedAt)
}

// notEvaluatedDecision records deliberate non-evaluation of a stale or busy Skip.
// A configured definition retains its Condition snapshot without an evaluation;
// an unconditioned definition records not_configured. A Skip never requests a
// bypass, so automatic outcomes always log bypass_requested=false.
func notEvaluatedDecision(
	conditions *automations.Condition,
) automations.ConditionDecision {
	if conditions == nil {
		return automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotConfigured,
		}
	}
	return automations.ConditionDecision{
		Mode:     automations.ConditionDecisionNotEvaluated,
		Snapshot: conditions,
	}
}

// writeReceipt records one matched-Fact outcome so a redelivered Fact never
// creates a second Run or Skip for the same Automation, even after the
// explanatory history row is pruned.
func (repo *AutomationRepository) writeReceipt(
	ctx context.Context,
	queries *dbsqlc.Queries,
	factID devices.DeviceFactID,
	automationID automations.AutomationID,
	kind automations.HistoryKind,
	historyID string,
) error {
	return queries.CreateFactReceipt(ctx, dbsqlc.CreateFactReceiptParams{
		FactID:       string(factID),
		AutomationID: string(automationID),
		OutcomeKind:  string(kind),
		HistoryID:    historyID,
	})
}
