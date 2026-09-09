package automations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/dbsqlc"
)

// automationScheduleCommitAttempts bounds final-clock rollover retries. Each
// stale attempt rolls back without persisting anything, so a repeatedly
// advancing clock cannot spin forever; the next tick then records a gap.
const automationScheduleCommitAttempts = 4

// errAutomationScheduleStale rolls back an evaluation whose minute stopped
// being current before commit. It never escapes the repository: the caller
// retries from fresh time.
var errAutomationScheduleStale = errors.New("automation schedule minute stale")

// automationFirstMinuteAfter reports the first whole UTC minute strictly after
// a definition write. Creation, schedule edits, and enablement changes become
// eligible only at a strictly later UTC minute boundary than their write time.
func automationFirstMinuteAfter(write time.Time) time.Time {
	return write.UTC().Truncate(time.Minute).Add(time.Minute)
}

// InitializeAutomationScheduler loads persisted progress, records any
// unevaluated interval through the startup minute as a core_restart gap, and
// advances progress to the later of the previous high-water mark and the
// startup minute. A fresh database establishes its baseline without inventing
// historical missed occurrences. Backward movement records no gap and keeps
// the high-water mark, so already-evaluated minutes can never replay.
func (repo *SQLiteRepository) InitializeAutomationScheduler(
	ctx context.Context,
	now time.Time,
	location *time.Location,
) (AutomationSchedulerState, error) {
	var state AutomationSchedulerState
	if location == nil {
		return state, fmt.Errorf("%w: household timezone required", ErrInvalidAutomation)
	}
	minute := now.UTC().Truncate(time.Minute)
	timezone := location.String()
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		next, err := initializeAutomationSchedulerState(ctx, q, minute, timezone, now.UTC())
		if err != nil {
			return err
		}
		state = next
		return nil
	})
	return state, err
}

// initializeAutomationSchedulerState resolves one startup instant against
// persisted progress: a fresh baseline, an unchanged high-water mark for the
// current or backward minute, or a core_restart gap through a forward minute.
func initializeAutomationSchedulerState(
	ctx context.Context,
	q *dbsqlc.Queries,
	minute time.Time,
	timezone string,
	recordedAt time.Time,
) (AutomationSchedulerState, error) {
	var state AutomationSchedulerState
	row, err := q.GetAutomationSchedulerState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		if err = q.UpsertAutomationSchedulerState(
			ctx,
			dbsqlc.UpsertAutomationSchedulerStateParams{
				HighWaterMinute: automationTime(minute),
				Timezone:        timezone,
			},
		); err != nil {
			return state, err
		}
		return AutomationSchedulerState{HighWaterMinute: minute, Timezone: timezone}, nil
	}
	if err != nil {
		return state, err
	}
	highWater, err := time.Parse(automationTimestampLayout, row.HighWaterMinute)
	if err != nil {
		return state, err
	}
	if !minute.After(highWater) {
		if err = q.UpsertAutomationSchedulerState(
			ctx,
			dbsqlc.UpsertAutomationSchedulerStateParams{
				HighWaterMinute: automationTime(highWater),
				Timezone:        timezone,
			},
		); err != nil {
			return state, err
		}
		return AutomationSchedulerState{HighWaterMinute: highWater, Timezone: timezone}, nil
	}
	if _, err = createAutomationScheduleGap(
		ctx,
		q,
		highWater,
		minute,
		recordedAt,
		AutomationScheduleGapCoreRestart,
	); err != nil {
		return state, err
	}
	state = AutomationSchedulerState{HighWaterMinute: minute, Timezone: timezone}
	if err = q.UpsertAutomationSchedulerState(
		ctx,
		dbsqlc.UpsertAutomationSchedulerStateParams{
			HighWaterMinute: automationTime(minute),
			Timezone:        timezone,
		},
	); err != nil {
		return AutomationSchedulerState{}, err
	}
	return state, nil
}

// EvaluateAutomationMinute atomically processes the current UTC minute only.
// It collects every matching Trigger snapshot in definition array order for
// each eligible Automation before making one shared admission decision, then
// commits one Occurrence plus one admitted Run snapshot or one overlap skip
// and advances the high-water mark in the same transaction. Duplicate and
// backward minutes are no-ops. A stale evaluated minute rolls back and
// retries from fresh time; failed persistence dispatches nothing and leaves
// progress unchanged.
func (repo *SQLiteRepository) EvaluateAutomationMinute(
	ctx context.Context,
	now time.Time,
	location *time.Location,
) (AutomationScheduleBatch, error) {
	batch := AutomationScheduleBatch{Runs: []AutomationRunRecord{}, Occurrences: []AutomationOccurrence{}}
	if location == nil {
		return batch, fmt.Errorf("%w: household timezone required", ErrInvalidAutomation)
	}
	minute := now.UTC().Truncate(time.Minute)
	for attempt := range automationScheduleCommitAttempts {
		result, stale, err := repo.evaluateAutomationMinuteOnce(ctx, minute, location)
		if err != nil {
			return batch, err
		}
		if !stale {
			return result, nil
		}
		if attempt+1 >= automationScheduleCommitAttempts {
			return batch, fmt.Errorf("automation schedule minute rolled over before commit")
		}
		minute = repo.scheduleClock()().UTC().Truncate(time.Minute)
	}
	return batch, fmt.Errorf("automation schedule minute rolled over before commit")
}

func (repo *SQLiteRepository) evaluateAutomationMinuteOnce(
	ctx context.Context,
	minute time.Time,
	location *time.Location,
) (AutomationScheduleBatch, bool, error) {
	batch := AutomationScheduleBatch{Runs: []AutomationRunRecord{}, Occurrences: []AutomationOccurrence{}}
	evaluated := false
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		row, err := q.GetAutomationSchedulerState(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: scheduler not initialized", ErrAutomationUnavailable)
		}
		if err != nil {
			return err
		}
		highWater, err := time.Parse(automationTimestampLayout, row.HighWaterMinute)
		if err != nil {
			return err
		}
		if !minute.After(highWater) {
			return nil
		}
		// A committed minute evaluation, even one that matches nothing, counts
		// as evaluated so scheduler health can distinguish it from a no-op.
		evaluated = true
		admissions, err := planScheduledMinute(ctx, q, minute, location)
		if err != nil {
			return err
		}
		// Sample evaluation time for stored timestamps. The final currency
		// check below re-samples after all writes, immediately before commit.
		commitNow := repo.scheduleClock()().UTC()
		if !commitNow.Truncate(time.Minute).Equal(minute) {
			return errAutomationScheduleStale
		}
		if err = q.UpsertAutomationSchedulerState(
			ctx,
			dbsqlc.UpsertAutomationSchedulerStateParams{
				HighWaterMinute: automationTime(minute),
				Timezone:        location.String(),
			},
		); err != nil {
			return err
		}
		if err = commitScheduledMinute(
			ctx,
			q,
			repo.newRunID,
			admissions,
			minute,
			highWater,
			commitNow,
			location.String(),
			&batch,
		); err != nil {
			return err
		}
		// Final fresh sample after all writes, immediately before commit. When
		// the evaluated minute stopped being current during writes, roll back
		// and retry from fresh time instead of committing a stale minute.
		if finalNow := repo.scheduleClock()().UTC(); !finalNow.Truncate(time.Minute).Equal(minute) {
			return errAutomationScheduleStale
		}
		return nil
	})
	if errors.Is(err, errAutomationScheduleStale) {
		return AutomationScheduleBatch{Runs: []AutomationRunRecord{}, Occurrences: []AutomationOccurrence{}}, true, nil
	}
	if err == nil {
		batch.Evaluated = evaluated
	}
	return batch, false, err
}

// scheduledMinuteAdmission is one Automation's collected minute matches in
// definition array order, admitted exactly once through shared admission.
type scheduledMinuteAdmission struct {
	record  AutomationRecord
	matched []AutomationTrigger
	ids     []AutomationTriggerID
}

// planScheduledMinute reads the eligible definitions and collects every
// matching Trigger snapshot before any admission decision. The fold check is
// independent of cron field matching: it is computed once per household
// minute and reused across every definition.
func planScheduledMinute(
	ctx context.Context,
	q *dbsqlc.Queries,
	minute time.Time,
	location *time.Location,
) ([]scheduledMinuteAdmission, error) {
	definitions, err := q.ListScheduleEligibleAutomations(
		ctx,
		dbsqlc.ListScheduleEligibleAutomationsParams{Minute: automationTime(minute)},
	)
	if err != nil {
		return nil, err
	}
	firstFold := cronFirstFoldUTCInstant(minute, location)
	local := minute.In(location)
	admissions := make([]scheduledMinuteAdmission, 0, len(definitions))
	for _, definition := range definitions {
		record, recordErr := automationRecord(definition)
		if recordErr != nil {
			return nil, recordErr
		}
		matched, ids, matchErr := matchScheduledTriggers(record.Definition.Triggers, local, firstFold)
		if matchErr != nil {
			return nil, matchErr
		}
		if len(matched) > 0 {
			admissions = append(admissions, scheduledMinuteAdmission{record: record, matched: matched, ids: ids})
		}
	}
	return admissions, nil
}

// commitScheduledMinute writes the evaluated minute: one bounded gap for any
// skipped interval, then one shared admission per collected Automation. The
// high-water mark was already advanced by the caller in this transaction.
func commitScheduledMinute(
	ctx context.Context,
	q *dbsqlc.Queries,
	newRunID func() (AutomationRunID, error),
	admissions []scheduledMinuteAdmission,
	minute, highWater, commitNow time.Time,
	timezone string,
	batch *AutomationScheduleBatch,
) error {
	if minute.After(highWater.Add(time.Minute)) {
		gap, err := createAutomationScheduleGap(
			ctx,
			q,
			highWater,
			minute.Add(-time.Minute),
			commitNow,
			AutomationScheduleGapClockOrProcessing,
		)
		if err != nil {
			return err
		}
		batch.Gap = &gap
	}
	for _, admission := range admissions {
		occurrence, run, err := admitScheduledAutomation(
			ctx,
			q,
			newRunID,
			admission.record,
			admission.matched,
			admission.ids,
			minute,
			commitNow,
			timezone,
		)
		if err != nil {
			return err
		}
		batch.Occurrences = append(batch.Occurrences, occurrence)
		if run != nil {
			batch.Runs = append(batch.Runs, *run)
		}
	}
	return nil
}

// admitScheduledAutomation performs the existing shared admission decision
// exactly once for one Automation's collected minute matches, atomically
// recording one Occurrence plus one admitted Run snapshot or one overlap skip.
// A running Run records one overlap skip containing all matching snapshots;
// nothing is queued and no per-Trigger skip is recorded.
func admitScheduledAutomation(
	ctx context.Context,
	q *dbsqlc.Queries,
	newRunID func() (AutomationRunID, error),
	record AutomationRecord,
	matched []AutomationTrigger,
	ids []AutomationTriggerID,
	minute time.Time,
	evaluatedAt time.Time,
	timezone string,
) (AutomationOccurrence, *AutomationRunRecord, error) {
	var occurrence AutomationOccurrence
	matchedJSON, err := json.Marshal(automationTriggerSnapshots(matched))
	if err != nil {
		return occurrence, nil, err
	}
	occurrence = AutomationOccurrence{
		AutomationID:    record.ID,
		Revision:        record.Revision,
		Name:            record.Definition.Name,
		MatchedTriggers: append([]AutomationTrigger(nil), matched...),
		Timezone:        timezone,
		ScheduledAt:     minute,
		EvaluatedAt:     evaluatedAt,
	}
	if err = checkAutomationInactive(ctx, q, record.ID); err != nil {
		if !errors.Is(err, ErrAutomationRunActive) {
			return occurrence, nil, err
		}
		reason := string(AutomationOccurrenceSkipActive)
		occurrence.Status = AutomationOccurrenceSkipped
		occurrence.SkipReason = &reason
		return occurrence, nil, q.CreateAutomationOccurrence(
			ctx,
			dbsqlc.CreateAutomationOccurrenceParams{
				AutomationID:        string(record.ID),
				ScheduledAt:         automationTime(minute),
				Revision:            record.Revision,
				Name:                record.Definition.Name,
				MatchedTriggersJson: string(matchedJSON),
				Timezone:            timezone,
				EvaluatedAt:         automationTime(evaluatedAt),
				Status:              string(AutomationOccurrenceSkipped),
				SkipReason:          automationString(reason),
			},
		)
	}
	runID, err := newRunID()
	if err != nil {
		return occurrence, nil, err
	}
	run := AutomationRunRecord{
		ID: runID,
		Snapshot: AutomationRunSnapshot{
			AutomationID: record.ID,
			Revision:     record.Revision,
			Definition:   record.Definition,
			Timezone:     timezone,
		},
		Source:            AutomationRunSourceScheduled,
		ScheduledAt:       &minute,
		MatchedTriggerIDs: append([]AutomationTriggerID(nil), ids...),
		Status:            AutomationRunStatusRunning,
		StartedAt:         evaluatedAt,
	}
	if err = createAutomationRun(ctx, q, run, nil); err != nil {
		return occurrence, nil, err
	}
	saved, err := q.GetAutomationRun(ctx, dbsqlc.GetAutomationRunParams{ID: string(runID)})
	if err != nil {
		return occurrence, nil, err
	}
	stored, err := readAutomationRun(ctx, q, saved)
	if err != nil {
		return occurrence, nil, err
	}
	occurrence.Status = AutomationOccurrenceStarted
	occurrence.RunID = &stored.ID
	if err = q.CreateAutomationOccurrence(
		ctx,
		dbsqlc.CreateAutomationOccurrenceParams{
			AutomationID:        string(record.ID),
			ScheduledAt:         automationTime(minute),
			Revision:            record.Revision,
			Name:                record.Definition.Name,
			MatchedTriggersJson: string(matchedJSON),
			Timezone:            timezone,
			EvaluatedAt:         automationTime(evaluatedAt),
			Status:              string(AutomationOccurrenceStarted),
			RunID:               automationString(string(stored.ID)),
		},
	); err != nil {
		return occurrence, nil, err
	}
	return occurrence, &stored, nil
}

func createAutomationScheduleGap(
	ctx context.Context,
	q *dbsqlc.Queries,
	fromExclusive time.Time,
	throughInclusive time.Time,
	recordedAt time.Time,
	reason string,
) (AutomationScheduleGap, error) {
	var gap AutomationScheduleGap
	id, err := NewAutomationScheduleGapID()
	if err != nil {
		return gap, err
	}
	gap = AutomationScheduleGap{
		ID:               id,
		FromExclusive:    fromExclusive,
		ThroughInclusive: throughInclusive,
		RecordedAt:       recordedAt,
		Reason:           reason,
	}
	return gap, q.CreateAutomationScheduleGap(
		ctx,
		dbsqlc.CreateAutomationScheduleGapParams{
			ID:               gap.ID,
			FromExclusive:    automationTime(fromExclusive),
			ThroughInclusive: automationTime(throughInclusive),
			RecordedAt:       automationTime(recordedAt),
			Reason:           reason,
		},
	)
}

func automationTriggerSnapshots(triggers []AutomationTrigger) []automationTriggerJSON {
	snapshots := make([]automationTriggerJSON, len(triggers))
	for i, trigger := range triggers {
		snapshots[i] = automationTriggerJSON(trigger)
	}
	return snapshots
}

// ListAutomationOccurrences fetches limit+1 in descending schedule order.
func (repo *SQLiteRepository) ListAutomationOccurrences(
	ctx context.Context,
	input AutomationOccurrenceListParams,
) (AutomationPage[AutomationOccurrence], error) {
	page := AutomationPage[AutomationOccurrence]{Items: []AutomationOccurrence{}}
	limit, limitErr := automationPageLimit(input.Limit)
	if limitErr != nil {
		return page, limitErr
	}
	if (input.BeforeScheduledAt == nil) != (input.BeforeAutomationID == nil) {
		return page, fmt.Errorf("%w: occurrence cursor requires time and automation", ErrInvalidAutomation)
	}
	if input.AutomationID != nil {
		if _, err := ParseAutomationID(string(*input.AutomationID)); err != nil {
			return page, err
		}
	}
	if input.BeforeAutomationID != nil {
		if _, err := ParseAutomationID(string(*input.BeforeAutomationID)); err != nil {
			return page, err
		}
	}
	params := dbsqlc.ListAutomationOccurrencesParams{
		AutomationFilter: automationNullableString(input.AutomationID),
		BeforeTime:       automationNullableTime(input.BeforeScheduledAt),
		BeforeID:         automationNullableString(input.BeforeAutomationID),
		PageLimit:        int64(limit + 1),
	}
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		rows, err := q.ListAutomationOccurrences(ctx, params)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			page.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			occurrence, decodeErr := decodeAutomationOccurrence(row)
			if decodeErr != nil {
				return decodeErr
			}
			page.Items = append(page.Items, occurrence)
		}
		return nil
	})
	return page, err
}

func decodeAutomationOccurrence(row dbsqlc.AutomationOccurrence) (AutomationOccurrence, error) {
	var occurrence AutomationOccurrence
	occurrence.AutomationID = AutomationID(row.AutomationID)
	occurrence.Revision = row.Revision
	occurrence.Name = row.Name
	var snapshots []automationTriggerJSON
	if err := json.Unmarshal([]byte(row.MatchedTriggersJson), &snapshots); err != nil {
		return occurrence, fmt.Errorf("automation stored occurrence triggers: %w", err)
	}
	occurrence.MatchedTriggers = make([]AutomationTrigger, len(snapshots))
	for i, snapshot := range snapshots {
		occurrence.MatchedTriggers[i] = AutomationTrigger(snapshot)
	}
	occurrence.Timezone = row.Timezone
	var err error
	occurrence.ScheduledAt, err = time.Parse(automationTimestampLayout, row.ScheduledAt)
	if err != nil {
		return occurrence, err
	}
	occurrence.EvaluatedAt, err = time.Parse(automationTimestampLayout, row.EvaluatedAt)
	if err != nil {
		return occurrence, err
	}
	occurrence.Status = AutomationOccurrenceStatus(row.Status)
	occurrence.RunID = automationPointer[AutomationRunID](row.RunID)
	occurrence.SkipReason = automationPointer[string](row.SkipReason)
	return occurrence, nil
}

// ListAutomationScheduleGaps fetches limit+1 in descending recording order.
func (repo *SQLiteRepository) ListAutomationScheduleGaps(
	ctx context.Context,
	input AutomationScheduleGapListParams,
) (AutomationPage[AutomationScheduleGap], error) {
	page := AutomationPage[AutomationScheduleGap]{Items: []AutomationScheduleGap{}}
	limit, limitErr := automationPageLimit(input.Limit)
	if limitErr != nil {
		return page, limitErr
	}
	if (input.BeforeRecordedAt == nil) != (input.BeforeID == nil) {
		return page, fmt.Errorf("%w: gap cursor requires time and ID", ErrInvalidAutomation)
	}
	if input.BeforeID != nil {
		if _, err := ParseAutomationScheduleGapID(*input.BeforeID); err != nil {
			return page, err
		}
	}
	params := dbsqlc.ListAutomationScheduleGapsParams{
		BeforeTime: automationNullableTime(input.BeforeRecordedAt),
		BeforeID:   automationNullableString(input.BeforeID),
		PageLimit:  int64(limit + 1),
	}
	err := repo.transaction(ctx, func(q *dbsqlc.Queries) error {
		rows, err := q.ListAutomationScheduleGaps(ctx, params)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			page.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			gap, decodeErr := decodeAutomationScheduleGap(row)
			if decodeErr != nil {
				return decodeErr
			}
			page.Items = append(page.Items, gap)
		}
		return nil
	})
	return page, err
}

func decodeAutomationScheduleGap(row dbsqlc.AutomationScheduleGap) (AutomationScheduleGap, error) {
	var gap AutomationScheduleGap
	gap.ID = row.ID
	var err error
	gap.FromExclusive, err = time.Parse(automationTimestampLayout, row.FromExclusive)
	if err != nil {
		return gap, err
	}
	gap.ThroughInclusive, err = time.Parse(automationTimestampLayout, row.ThroughInclusive)
	if err != nil {
		return gap, err
	}
	gap.RecordedAt, err = time.Parse(automationTimestampLayout, row.RecordedAt)
	if err != nil {
		return gap, err
	}
	gap.Reason = row.Reason
	return gap, nil
}
