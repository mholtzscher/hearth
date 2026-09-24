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

type storedHeldState struct {
	triggerID sql.NullString
	startedAt sql.NullString
	dueAt     sql.NullString
}

func storedHeldStateColumns(evidence *automations.HeldStateEvidence) storedHeldState {
	if evidence == nil {
		return storedHeldState{}
	}
	return storedHeldState{
		triggerID: sql.NullString{String: string(evidence.TriggerID), Valid: true},
		startedAt: sql.NullString{String: encodeAutomationTimestamp(evidence.StartedAt), Valid: true},
		dueAt:     sql.NullString{String: encodeAutomationTimestamp(evidence.DueAt), Valid: true},
	}
}

// updateHeldStateFacts advances receive-order cursors and updates matching holds in the Fact transaction.
func (repo *AutomationRepository) updateHeldStateFacts(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.Record,
	fact *automations.ObservationFact,
	admittedAt time.Time,
	startupAt time.Time,
) error {
	if fact == nil {
		return nil
	}
	triggers := heldStateTriggersForEntity(record.Definition.Triggers, fact.EntityID)
	if len(triggers) == 0 {
		return nil
	}
	observation := dbsqlc.GetHeldStateObservationReceiveOrderParams{
		ObservationID: string(fact.ObservationID),
	}
	receiveOrder, err := queries.GetHeldStateObservationReceiveOrder(ctx, observation)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Observation history may be pruned before a delayed Fact is delivered.
			// Without its receive order it cannot safely advance a hold cursor;
			// leave immediate Fact admission independent of history retention.
			return nil
		}
		return err
	}
	for _, trigger := range triggers {
		if updateErr := repo.updateHeldStateFact(
			ctx, queries, record, trigger, fact, admittedAt, startupAt, receiveOrder,
		); updateErr != nil {
			return updateErr
		}
	}
	return nil
}

func heldStateTriggersForEntity(triggers []automations.Trigger, entityID devices.EntityID) []automations.Trigger {
	matching := make([]automations.Trigger, 0, len(triggers))
	for _, trigger := range triggers {
		if trigger.Kind == automations.TriggerKindHeldState && trigger.HeldState != nil &&
			trigger.HeldState.EntityID == entityID {
			matching = append(matching, trigger)
		}
	}
	return matching
}

func (repo *AutomationRepository) updateHeldStateFact(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.Record,
	trigger automations.Trigger,
	fact *automations.ObservationFact,
	admittedAt time.Time,
	startupAt time.Time,
	receiveOrder int64,
) error {
	matched, err := automations.MatchHeldState(*trigger.HeldState, fact.Value)
	if err != nil {
		return err
	}
	eligible := matched && admittedAt.Sub(fact.EmittedAt) <= automations.FactMaximumAge &&
		fact.EmittedAt.After(record.UpdatedAt) && fact.EmittedAt.After(startupAt)
	if !matched {
		return queries.CancelHeldStateFact(ctx, dbsqlc.CancelHeldStateFactParams{
			AutomationID: string(record.ID), Revision: record.Revision,
			TriggerID: string(trigger.ID), LastReceiveOrder: receiveOrder,
		})
	}
	if !eligible {
		return queries.AdvanceStaleHeldStateFact(ctx, dbsqlc.AdvanceStaleHeldStateFactParams{
			AutomationID: string(record.ID), Revision: record.Revision,
			TriggerID: string(trigger.ID), LastReceiveOrder: receiveOrder,
		})
	}
	duration, err := automations.HeldStateDuration(trigger.HeldState.ForSeconds)
	if err != nil {
		return err
	}
	return queries.StartHeldStateFact(ctx, dbsqlc.StartHeldStateFactParams{
		AutomationID: string(record.ID), Revision: record.Revision,
		TriggerID: string(trigger.ID), LastReceiveOrder: receiveOrder,
		StartedAt: sql.NullString{String: encodeAutomationTimestamp(fact.EmittedAt), Valid: true},
		DueAt:     sql.NullString{String: encodeAutomationTimestamp(fact.EmittedAt.Add(duration)), Valid: true},
	})
}

// ListDueHeldStates returns up to limit pending holds ordered by deadline and identity.
func (repo *AutomationRepository) ListDueHeldStates(
	ctx context.Context,
	at time.Time,
	limit int,
) ([]automations.HeldStateCandidate, error) {
	if at.IsZero() || limit < 1 {
		return nil, fmt.Errorf("%w: due time and positive limit are required", automations.ErrInvalidAutomation)
	}
	rows, err := repo.queries.ListDueHeldStateCandidates(ctx, dbsqlc.ListDueHeldStateCandidatesParams{
		DueAt: sql.NullString{String: encodeAutomationTimestamp(at), Valid: true}, Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]automations.HeldStateCandidate, 0, limit)
	for _, row := range rows {
		var candidate automations.HeldStateCandidate
		candidate.AutomationID = automations.AutomationID(row.AutomationID)
		candidate.Revision = row.Revision
		candidate.TriggerID = automations.TriggerID(row.TriggerID)
		candidate.DueAt, err = decodeAutomationTimestamp(row.DueAt.String)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// AdmitDueHeldStates rechecks due holds, definitions, current State, and busy/Condition outcomes atomically.
func (repo *AutomationRepository) AdmitDueHeldStates(
	ctx context.Context,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
	limit int,
) (automations.AdmissionResult, int, error) {
	if at.IsZero() || limit < 1 {
		return automations.AdmissionResult{}, 0, fmt.Errorf(
			"%w: due time and positive limit are required", automations.ErrInvalidAutomation,
		)
	}
	var result automations.AdmissionResult
	processed := 0
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		holds, queryErr := queries.ListDueHeldStateRows(ctx, dbsqlc.ListDueHeldStateRowsParams{
			DueAt: sql.NullString{String: encodeAutomationTimestamp(at), Valid: true}, Limit: int64(limit),
		})
		if queryErr != nil {
			return queryErr
		}
		for _, hold := range holds {
			processed++
			if err := repo.admitDueHeldState(ctx, queries, hold, snapshot, at, &result); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return automations.AdmissionResult{}, 0, err
	}
	return result, processed, nil
}

func (repo *AutomationRepository) admitDueHeldState(
	ctx context.Context,
	queries *dbsqlc.Queries,
	hold dbsqlc.AutomationHold,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
	result *automations.AdmissionResult,
) error {
	row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: hold.AutomationID})
	if errors.Is(err, sql.ErrNoRows) {
		return deleteHeldState(ctx, queries, hold)
	}
	if err != nil {
		return err
	}
	record, err := automationRecord(row)
	if err != nil {
		return err
	}
	triggerID := automations.TriggerID(hold.TriggerID)
	var trigger *automations.Trigger
	for i := range record.Definition.Triggers {
		if record.Definition.Triggers[i].ID == triggerID {
			trigger = &record.Definition.Triggers[i]
			break
		}
	}
	if record.Revision != hold.Revision || !record.Definition.Enabled || trigger == nil ||
		trigger.Kind != automations.TriggerKindHeldState || trigger.HeldState == nil {
		return deleteHeldState(ctx, queries, hold)
	}
	state, err := queries.GetHeldStateEntityState(ctx, dbsqlc.GetHeldStateEntityStateParams{
		EntityID: string(trigger.HeldState.EntityID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return cancelHeldState(ctx, queries, hold, nil)
	}
	if err != nil {
		return err
	}
	matched, err := automations.MatchHeldState(*trigger.HeldState, devices.Value(state.ValueJson))
	if err != nil {
		return fmt.Errorf("decode current held-state Entity State: %w", err)
	}
	if !matched {
		return cancelHeldState(ctx, queries, hold, &state.ReceiveOrder)
	}
	running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{AutomationID: hold.AutomationID})
	if err != nil {
		return err
	}
	evidence, err := heldEvidence(hold)
	if err != nil {
		return err
	}
	decision, reason, err := dueHeldStateDecision(record.Definition.Conditions, running, snapshot, at)
	if err != nil {
		return err
	}
	if err = repo.persistDueHeldStateOutcome(
		ctx, queries, record, triggerID, evidence, decision, reason, at, result,
	); err != nil {
		return err
	}
	result.Outcome.MatchedAutomations++
	return queries.ConsumeDueHeldState(ctx, dbsqlc.ConsumeDueHeldStateParams{
		MAX: state.ReceiveOrder, AutomationID: hold.AutomationID,
		TriggerID: hold.TriggerID, Revision: hold.Revision,
		DueAt: sql.NullString{String: encodeAutomationTimestamp(at), Valid: true},
	})
}

func dueHeldStateDecision(
	conditions *automations.Condition,
	running int64,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (automations.ConditionDecision, automations.SkipReason, error) {
	switch {
	case running > 0:
		return heldNotEvaluatedDecision(conditions), automations.SkipBusy, nil
	case conditions == nil:
		return automations.NotConfiguredDecision(), "", nil
	default:
		return automations.DecideConditions(conditions, snapshot, at)
	}
}

func (repo *AutomationRepository) persistDueHeldStateOutcome(
	ctx context.Context,
	queries *dbsqlc.Queries,
	record automations.Record,
	triggerID automations.TriggerID,
	evidence automations.HeldStateEvidence,
	decision automations.ConditionDecision,
	reason automations.SkipReason,
	at time.Time,
	result *automations.AdmissionResult,
) error {
	if reason != "" {
		skipID, idErr := repo.newSkipID()
		if idErr != nil {
			return fmt.Errorf("allocate automation skip ID: %w", idErr)
		}
		triggers, snapshotErr := automations.MatchedTriggerSnapshots(
			record.Definition, []automations.TriggerID{triggerID},
		)
		if snapshotErr != nil {
			return snapshotErr
		}
		skip := automations.Skip{
			ID: skipID, AutomationID: record.ID, AutomationName: record.Definition.Name,
			Revision: record.Revision, Source: automations.RunSourceHeldState, HeldState: &evidence,
			MatchedTriggers: triggers, Reason: reason, ConditionDecision: decision, SkippedAt: at.UTC(),
		}
		if err := repo.persistHistorySkip(ctx, queries, skip); err != nil {
			return err
		}
		result.Skips = append(result.Skips, automations.AdmissionSkip{
			SkipID: skipID, AutomationID: record.ID, Revision: record.Revision,
			Source: automations.RunSourceHeldState, Reason: reason,
		})
		result.Outcome.RecordedSkips++
		return nil
	}
	runID, err := repo.newRunID()
	if err != nil {
		return fmt.Errorf("allocate automation run ID: %w", err)
	}
	run := automations.NewRunSnapshot(record, runID, automations.RunSourceHeldState, nil,
		[]automations.TriggerID{triggerID}, decision, at)
	run.HeldState = &evidence
	if err = repo.persistRun(ctx, queries, run); err != nil {
		return err
	}
	result.StartedRuns = append(result.StartedRuns, run)
	result.Outcome.StartedRuns++
	return nil
}

func heldEvidence(hold dbsqlc.AutomationHold) (automations.HeldStateEvidence, error) {
	started, err := decodeAutomationTimestamp(hold.StartedAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	due, err := decodeAutomationTimestamp(hold.DueAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	return automations.HeldStateEvidence{
		TriggerID: automations.TriggerID(hold.TriggerID),
		StartedAt: started,
		DueAt:     due,
	}, nil
}

func heldNotEvaluatedDecision(conditions *automations.Condition) automations.ConditionDecision {
	if conditions == nil {
		return automations.NotConfiguredDecision()
	}
	return automations.NotEvaluatedDecision(*conditions)
}

func cancelHeldState(
	ctx context.Context,
	queries *dbsqlc.Queries,
	hold dbsqlc.AutomationHold,
	receiveOrder *int64,
) error {
	if receiveOrder == nil {
		return queries.CancelHeldStateWithoutState(ctx, dbsqlc.CancelHeldStateWithoutStateParams{
			AutomationID: hold.AutomationID, TriggerID: hold.TriggerID,
		})
	}
	return queries.CancelHeldStateWithReceiveOrder(ctx, dbsqlc.CancelHeldStateWithReceiveOrderParams{
		MAX: *receiveOrder, AutomationID: hold.AutomationID, TriggerID: hold.TriggerID,
	})
}

func deleteHeldState(ctx context.Context, queries *dbsqlc.Queries, hold dbsqlc.AutomationHold) error {
	return queries.DeleteHeldState(ctx, dbsqlc.DeleteHeldStateParams{
		AutomationID: hold.AutomationID, TriggerID: hold.TriggerID,
	})
}

// ResetPendingHeldStates clears deadlines after restart while preserving cursor watermarks and consumed holds.
func (repo *AutomationRepository) ResetPendingHeldStates(ctx context.Context) error {
	return repo.queries.ResetPendingHeldStates(ctx)
}
