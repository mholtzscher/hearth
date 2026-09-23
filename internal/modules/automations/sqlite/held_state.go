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

type heldStateRow struct {
	automationID     string
	revision         int64
	triggerID        string
	lastReceiveOrder int64
	phase            string
	startedAt        sql.NullString
	dueAt            sql.NullString
}

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
	tx *sql.Tx,
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
	var receiveOrder int64
	if err := tx.QueryRowContext(ctx,
		`SELECT receive_order FROM observations WHERE observation_id = ?`, string(fact.ObservationID),
	).Scan(&receiveOrder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Observation history may be pruned before a delayed Fact is delivered.
			// Without its receive order it cannot safely advance a hold cursor;
			// leave immediate Fact admission independent of history retention.
			return nil
		}
		return err
	}
	for _, trigger := range triggers {
		if err := repo.updateHeldStateFact(
			ctx, tx, record, trigger, fact, admittedAt, startupAt, receiveOrder,
		); err != nil {
			return err
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
	tx *sql.Tx,
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
	initialPhase, initialStart, initialDue, err := initialHeldStateValues(trigger, fact, eligible)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO automation_holds (
			automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (automation_id, trigger_id) DO UPDATE SET
			revision = excluded.revision,
			last_receive_order = excluded.last_receive_order,
			phase = CASE WHEN ? = 0 THEN 'idle'
				WHEN automation_holds.phase = 'idle' AND ? = 1 THEN 'pending'
				ELSE automation_holds.phase END,
			started_at = CASE WHEN ? = 0 THEN NULL
				WHEN automation_holds.phase = 'idle' AND ? = 1 THEN excluded.started_at
				ELSE automation_holds.started_at END,
			due_at = CASE WHEN ? = 0 THEN NULL
				WHEN automation_holds.phase = 'idle' AND ? = 1 THEN excluded.due_at
				ELSE automation_holds.due_at END
		WHERE excluded.last_receive_order > automation_holds.last_receive_order`,
		string(record.ID), record.Revision, string(trigger.ID), receiveOrder, initialPhase, initialStart, initialDue,
		boolInt(matched), boolInt(eligible), boolInt(matched), boolInt(eligible), boolInt(matched), boolInt(eligible))
	return err
}

func initialHeldStateValues(
	trigger automations.Trigger,
	fact *automations.ObservationFact,
	eligible bool,
) (string, any, any, error) {
	if !eligible {
		return "idle", nil, nil, nil
	}
	duration, err := automations.HeldStateDuration(trigger.HeldState.ForSeconds)
	if err != nil {
		return "", nil, nil, err
	}
	return "pending", encodeAutomationTimestamp(fact.EmittedAt),
		encodeAutomationTimestamp(fact.EmittedAt.Add(duration)), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
	rows, err := repo.database.QueryContext(ctx, `SELECT automation_id, revision, trigger_id, due_at
		FROM automation_holds WHERE phase = 'pending' AND due_at <= ?
		ORDER BY due_at, automation_id, trigger_id LIMIT ?`, encodeAutomationTimestamp(at), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]automations.HeldStateCandidate, 0, limit)
	for rows.Next() {
		var candidate automations.HeldStateCandidate
		var automationID, triggerID, dueAt string
		if err = rows.Scan(&automationID, &candidate.Revision, &triggerID, &dueAt); err != nil {
			return nil, err
		}
		candidate.AutomationID = automations.AutomationID(automationID)
		candidate.TriggerID = automations.TriggerID(triggerID)
		candidate.DueAt, err = decodeAutomationTimestamp(dueAt)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
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
	err := repo.transactionWithTx(ctx, func(queries *dbsqlc.Queries, tx *sql.Tx) error {
		holds, queryErr := listDueHeldStateRows(ctx, tx, at, limit)
		if queryErr != nil {
			return queryErr
		}
		for _, hold := range holds {
			processed++
			if err := repo.admitDueHeldState(ctx, tx, queries, hold, snapshot, at, &result); err != nil {
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

func listDueHeldStateRows(
	ctx context.Context,
	tx *sql.Tx,
	at time.Time,
	limit int,
) ([]heldStateRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT automation_id, revision, trigger_id,
		last_receive_order, phase, started_at, due_at FROM automation_holds
		WHERE phase = 'pending' AND due_at <= ?
		ORDER BY due_at, automation_id, trigger_id LIMIT ?`, encodeAutomationTimestamp(at), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var holds []heldStateRow
	for rows.Next() {
		var hold heldStateRow
		if scanErr := rows.Scan(&hold.automationID, &hold.revision, &hold.triggerID,
			&hold.lastReceiveOrder, &hold.phase, &hold.startedAt, &hold.dueAt); scanErr != nil {
			return nil, scanErr
		}
		holds = append(holds, hold)
	}
	return holds, rows.Err()
}

func (repo *AutomationRepository) admitDueHeldState(
	ctx context.Context,
	tx *sql.Tx,
	queries *dbsqlc.Queries,
	hold heldStateRow,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
	result *automations.AdmissionResult,
) error {
	row, err := queries.GetAutomation(ctx, dbsqlc.GetAutomationParams{ID: hold.automationID})
	if errors.Is(err, sql.ErrNoRows) {
		return deleteHeldState(ctx, tx, hold)
	}
	if err != nil {
		return err
	}
	record, err := automationRecord(row)
	if err != nil {
		return err
	}
	triggerID := automations.TriggerID(hold.triggerID)
	var trigger *automations.Trigger
	for i := range record.Definition.Triggers {
		if record.Definition.Triggers[i].ID == triggerID {
			trigger = &record.Definition.Triggers[i]
			break
		}
	}
	if record.Revision != hold.revision || !record.Definition.Enabled || trigger == nil ||
		trigger.Kind != automations.TriggerKindHeldState || trigger.HeldState == nil {
		return deleteHeldState(ctx, tx, hold)
	}
	var value string
	var receiveOrder int64
	err = tx.QueryRowContext(ctx, `SELECT value_json, receive_order FROM entity_states WHERE entity_id = ?`,
		string(trigger.HeldState.EntityID)).Scan(&value, &receiveOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return cancelHeldState(ctx, tx, hold, nil)
	}
	if err != nil {
		return err
	}
	matched, err := automations.MatchHeldState(*trigger.HeldState, devices.Value(value))
	if err != nil {
		return fmt.Errorf("decode current held-state Entity State: %w", err)
	}
	if !matched {
		return cancelHeldState(ctx, tx, hold, &receiveOrder)
	}
	running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{AutomationID: hold.automationID})
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
	_, err = tx.ExecContext(ctx, `UPDATE automation_holds SET phase = 'consumed', started_at = NULL, due_at = NULL
		WHERE automation_id = ? AND trigger_id = ? AND revision = ? AND phase = 'pending'
		AND due_at <= ?`, hold.automationID, hold.triggerID, hold.revision, encodeAutomationTimestamp(at))
	return err
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

func heldEvidence(hold heldStateRow) (automations.HeldStateEvidence, error) {
	started, err := decodeAutomationTimestamp(hold.startedAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	due, err := decodeAutomationTimestamp(hold.dueAt.String)
	if err != nil {
		return automations.HeldStateEvidence{}, err
	}
	return automations.HeldStateEvidence{
		TriggerID: automations.TriggerID(hold.triggerID),
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

func cancelHeldState(ctx context.Context, tx *sql.Tx, hold heldStateRow, receiveOrder *int64) error {
	if receiveOrder == nil {
		_, err := tx.ExecContext(ctx, `UPDATE automation_holds SET phase = 'idle', started_at = NULL,
		due_at = NULL WHERE automation_id = ? AND trigger_id = ?`, hold.automationID, hold.triggerID)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE automation_holds SET phase = 'idle', started_at = NULL,
		due_at = NULL, last_receive_order = MAX(last_receive_order, ?)
		WHERE automation_id = ? AND trigger_id = ?`, *receiveOrder, hold.automationID, hold.triggerID)
	return err
}

func deleteHeldState(ctx context.Context, tx *sql.Tx, hold heldStateRow) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM automation_holds WHERE automation_id = ? AND trigger_id = ?`,
		hold.automationID, hold.triggerID)
	return err
}

// ResetPendingHeldStates clears deadlines after restart while preserving cursor watermarks and consumed holds.
func (repo *AutomationRepository) ResetPendingHeldStates(ctx context.Context) error {
	_, err := repo.database.ExecContext(ctx, `UPDATE automation_holds
		SET phase = 'idle', started_at = NULL, due_at = NULL
		WHERE phase = 'pending'`)
	return err
}
