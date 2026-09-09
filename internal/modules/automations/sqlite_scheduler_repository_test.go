package automations //nolint:testpackage // Scheduler tests inject repository clocks and storage faults.

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func scheduleMinute(hour, minute int) time.Time {
	return time.Date(2026, time.June, 1, hour, minute, 0, 0, time.UTC)
}

func setAutomationClocks(repo *SQLiteRepository, now time.Time) {
	repo.now = func() time.Time { return now }
	repo.scheduleNow = func() time.Time { return now }
}

func scheduleTrigger(id, expression string) AutomationTrigger {
	return AutomationTrigger{ID: AutomationTriggerID(id), Kind: AutomationTriggerKindCron, Expression: expression}
}

func createScheduledAutomation(
	t *testing.T,
	repo *SQLiteRepository,
	enabled bool,
	triggers ...AutomationTrigger,
) AutomationRecord {
	t.Helper()
	definition := testAutomationDefinition()
	definition.Enabled = enabled
	definition.Triggers = triggers
	record, err := repo.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func initializeSchedule(t *testing.T, repo *SQLiteRepository, now time.Time) AutomationSchedulerState {
	t.Helper()
	state, err := repo.InitializeAutomationScheduler(t.Context(), now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func evaluateSchedule(t *testing.T, repo *SQLiteRepository, now time.Time) AutomationScheduleBatch {
	t.Helper()
	setAutomationClocks(repo, now)
	batch, err := repo.EvaluateAutomationMinute(t.Context(), now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func countScheduleRows(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func queryScheduleBound(t *testing.T, database *sql.DB, id AutomationID) string {
	t.Helper()
	var bound string
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT schedule_not_before FROM automations WHERE id = ?`,
		string(id),
	).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	return bound
}

func queryScheduleHighWater(t *testing.T, database *sql.DB) string {
	t.Helper()
	var highWater string
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT high_water_minute FROM automation_scheduler_state WHERE scheduler_key = 1`,
	).Scan(&highWater); err != nil {
		t.Fatal(err)
	}
	return highWater
}

// This test protects schedule_not_before write bounds and fails if a mid-minute
// create, edit, or enablement back-triggers that minute.
func TestAutomationScheduleNotBeforeBounds(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(19, 0).Add(30*time.Second))
	automation := createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	if bound := queryScheduleBound(t, database, automation.ID); bound != automationTime(scheduleMinute(19, 1)) {
		t.Fatalf("create bound: %s", bound)
	}
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	// A mid-minute create cannot back-trigger its write minute.
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(45*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 || batch.Gap != nil {
		t.Fatalf("back-triggered write minute: %+v", batch)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(19, 0)) {
		t.Fatalf("high water: %s", highWater)
	}
	// The strictly later minute is eligible.
	batch := evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(10*time.Second))
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 || batch.Gap != nil {
		t.Fatalf("eligible minute: %+v", batch)
	}
	// An expression-preserving update resets the bound: a create racing the next
	// minute cannot back-trigger it either.
	setAutomationClocks(repo, scheduleMinute(19, 1).Add(30*time.Second))
	replacement := testAutomationDefinition()
	replacement.Enabled = true
	replacement.Triggers = []AutomationTrigger{scheduleTrigger("every", "* * * * *")}
	updated, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: replacement},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound := queryScheduleBound(t, database, automation.ID); bound != automationTime(scheduleMinute(19, 2)) {
		t.Fatalf("update bound: %s", bound)
	}
	late, err := repo.CreateAutomation(
		t.Context(),
		AutomationDefinition{
			Name:     "Late",
			Enabled:  true,
			Triggers: []AutomationTrigger{scheduleTrigger("every", "* * * * *")},
			Steps:    testAutomationDefinition().Steps,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = updated
	_ = late
	if batch = evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(50*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("updated definitions back-triggered: %+v", batch)
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 1 {
		t.Fatalf("occurrences: %d", count)
	}
}

// This test protects same-minute match collection and fails if matches admit
// per-trigger attempts, combine across minutes, replay, or duplicate.
func TestAutomationScheduleMultiTriggerCollection(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(
		t, repo, true,
		scheduleTrigger("evening", "0 19 * * *"),
		scheduleTrigger("hourly", "0 * * * *"),
		scheduleTrigger("later", "0 20 * * *"),
	)
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	batch := evaluateSchedule(t, repo, now)
	assertScheduleMultiTriggerBatch(t, batch, now)
	// A repeated tick for the same minute is a no-op.
	if repeat := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(50*time.Second)); len(repeat.Runs) != 0 ||
		len(repeat.Occurrences) != 0 {
		t.Fatalf("repeat tick: %+v", repeat)
	}
	// Complete the admitted Run so the next minute admits instead of skipping.
	interruptTestAutomation(t, repo, batch.Runs[0].ID)
	// Concurrent evaluation still yields one Occurrence/Run per Automation/minute.
	evaluateConcurrentSchedule(t, repo, scheduleMinute(20, 0).Add(5*time.Second))
	var evening, hourly int
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM automation_occurrences WHERE automation_id = ? AND scheduled_at = ?`,
		string(automation.ID),
		automationTime(scheduleMinute(20, 0)),
	).Scan(&evening); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM automation_runs WHERE automation_id = ? AND scheduled_at = ?`,
		string(automation.ID),
		automationTime(scheduleMinute(20, 0)),
	).Scan(&hourly); err != nil {
		t.Fatal(err)
	}
	if evening != 1 || hourly != 1 {
		t.Fatalf("concurrent duplicates: occurrences=%d runs=%d", evening, hourly)
	}
	stored, err := repo.ListAutomationOccurrences(
		t.Context(),
		AutomationOccurrenceListParams{AutomationID: &automation.ID},
	)
	if err != nil || len(stored.Items) != 2 {
		t.Fatalf("history: %+v %v", stored, err)
	}
	// Matches in different minutes are never combined: 20:00 collected hourly and
	// later, not the 19:00 evening subset.
	if len(stored.Items[0].MatchedTriggers) != 2 || stored.Items[0].MatchedTriggers[0].ID != "hourly" ||
		stored.Items[0].MatchedTriggers[1].ID != "later" {
		t.Fatalf("20:00 subset: %+v", stored.Items[0].MatchedTriggers)
	}
	// Backward movement does not replay processed minutes.
	if batch = evaluateSchedule(t, repo, scheduleMinute(19, 30)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("backward replay: %+v", batch)
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 2 {
		t.Fatalf("occurrences: %d", count)
	}
}

func assertScheduleMultiTriggerBatch(t *testing.T, batch AutomationScheduleBatch, now time.Time) {
	t.Helper()
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 || batch.Gap != nil {
		t.Fatalf("batch: %+v", batch)
	}
	occurrence := batch.Occurrences[0]
	if occurrence.Status != AutomationOccurrenceStarted || occurrence.RunID == nil || occurrence.SkipReason != nil ||
		occurrence.Revision != 1 || occurrence.Timezone != "UTC" ||
		!occurrence.ScheduledAt.Equal(scheduleMinute(19, 0)) ||
		!occurrence.EvaluatedAt.Equal(now.UTC()) {
		t.Fatalf("occurrence: %+v", occurrence)
	}
	if len(occurrence.MatchedTriggers) != 2 || occurrence.MatchedTriggers[0].ID != "evening" ||
		occurrence.MatchedTriggers[0].Expression != "0 19 * * *" ||
		occurrence.MatchedTriggers[1].ID != "hourly" || occurrence.MatchedTriggers[1].Kind != AutomationTriggerKindCron {
		t.Fatalf("matched subset: %+v", occurrence.MatchedTriggers)
	}
	run := batch.Runs[0]
	if run.Source != AutomationRunSourceScheduled || run.ScheduledAt == nil ||
		!run.ScheduledAt.Equal(scheduleMinute(19, 0)) ||
		len(run.MatchedTriggerIDs) != 2 || run.MatchedTriggerIDs[0] != "evening" ||
		run.MatchedTriggerIDs[1] != "hourly" || !run.StartedAt.Equal(now.UTC()) || len(run.Steps) != 2 {
		t.Fatalf("scheduled run: %+v", run)
	}
}

func evaluateConcurrentSchedule(t *testing.T, repo *SQLiteRepository, now time.Time) {
	t.Helper()
	const callers = 8
	setAutomationClocks(repo, now)
	var workers sync.WaitGroup
	batches := make(chan AutomationScheduleBatch, callers)
	failures := make(chan error, callers)
	for range callers {
		workers.Go(func() {
			batch, err := repo.EvaluateAutomationMinute(t.Context(), now, time.UTC)
			if err != nil {
				failures <- err
				return
			}
			batches <- batch
		})
	}
	workers.Wait()
	close(batches)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	admitted := 0
	for batch := range batches {
		admitted += len(batch.Occurrences)
	}
	if admitted != 1 {
		t.Fatalf("concurrent admissions: %d", admitted)
	}
}

// This test protects household-local matching and fails if cron fields match
// fixed UTC instead of the household timezone.
func TestAutomationScheduleMatchesHouseholdWallClock(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(t, repo, true, scheduleTrigger("afternoon", "0 15 * * *"))
	state, err := repo.InitializeAutomationScheduler(t.Context(), scheduleMinute(18, 59), location)
	if err != nil {
		t.Fatal(err)
	}
	if state.Timezone != "America/New_York" {
		t.Fatalf("state timezone: %+v", state)
	}
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	setAutomationClocks(repo, now)
	// 19:00Z is 15:00 in New York on 2026-06-01, so the local expression matches.
	batch, err := repo.EvaluateAutomationMinute(t.Context(), now, location)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 {
		t.Fatalf("local match: %+v", batch)
	}
	if batch.Occurrences[0].Timezone != "America/New_York" ||
		batch.Runs[0].Snapshot.Timezone != "America/New_York" {
		t.Fatalf("snapshot timezone: %+v", batch.Occurrences[0])
	}
	_ = automation
}

// This test protects shared overlap admission and fails if a blocked Automation
// queues work, records per-trigger skips, or blocks independent Automations.
func TestAutomationScheduleOverlapSkip(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	blocked := createScheduledAutomation(
		t, repo, true,
		scheduleTrigger("first", "* * * * *"),
		scheduleTrigger("second", "* * * * *"),
	)
	independent := createScheduledAutomation(t, repo, true, scheduleTrigger("other", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	manual := admitTestAutomation(t, repo, blocked.ID, "block")
	if len(manual.MatchedTriggerIDs) != 0 {
		t.Fatalf("manual matches: %+v", manual.MatchedTriggerIDs)
	}
	batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(20*time.Second))
	if len(batch.Occurrences) != 2 || len(batch.Runs) != 1 || batch.Gap != nil {
		t.Fatalf("overlap batch: %+v", batch)
	}
	var skipped, started *AutomationOccurrence
	for i := range batch.Occurrences {
		occurrence := &batch.Occurrences[i]
		switch occurrence.AutomationID {
		case blocked.ID:
			skipped = occurrence
		case independent.ID:
			started = occurrence
		}
	}
	if skipped == nil || started == nil {
		t.Fatalf("occurrences: %+v", batch.Occurrences)
	}
	if skipped.Status != AutomationOccurrenceSkipped || skipped.RunID != nil || skipped.SkipReason == nil ||
		*skipped.SkipReason != AutomationOccurrenceSkipActive || len(skipped.MatchedTriggers) != 2 ||
		skipped.MatchedTriggers[0].ID != "first" || skipped.MatchedTriggers[1].ID != "second" {
		t.Fatalf("skipped occurrence: %+v", skipped)
	}
	if started.Status != AutomationOccurrenceStarted || started.RunID == nil ||
		batch.Runs[0].ID != *started.RunID ||
		len(batch.Runs[0].MatchedTriggerIDs) != 1 || batch.Runs[0].MatchedTriggerIDs[0] != "other" {
		t.Fatalf("independent admission: %+v %+v", started, batch.Runs)
	}
	if count := countScheduleRows(t, database, "automation_runs"); count != 2 {
		t.Fatalf("queued work: %d runs", count)
	}
	// Once the blocking Runs complete, the next minute admits through the same path.
	interruptTestAutomation(t, repo, manual.ID)
	interruptTestAutomation(t, repo, batch.Runs[0].ID)
	batch = evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second))
	if len(batch.Runs) != 2 || len(batch.Occurrences) != 2 {
		t.Fatalf("recovered batch: %+v", batch)
	}
	for _, occurrence := range batch.Occurrences {
		if occurrence.Status != AutomationOccurrenceStarted {
			t.Fatalf("still skipped: %+v", occurrence)
		}
	}
}

// This test protects restart and gap semantics and fails if restarts execute
// historical minutes, invent per-minute runs, or replay backward movement.
func TestAutomationScheduleRestartGapAndBackward(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	if state := initializeSchedule(t, repo, scheduleMinute(19, 0)); !state.HighWaterMinute.Equal(
		scheduleMinute(19, 0),
	) {
		t.Fatalf("baseline: %+v", state)
	}
	if count := countScheduleRows(t, database, "automation_schedule_gaps"); count != 0 {
		t.Fatalf("fresh baseline gap: %d", count)
	}
	// Restarting inside the same minute records no gap and keeps progress.
	if state := initializeSchedule(t, repo, scheduleMinute(19, 0).Add(30*time.Second)); !state.HighWaterMinute.Equal(
		scheduleMinute(19, 0),
	) {
		t.Fatalf("same minute restart: %+v", state)
	}
	setAutomationClocks(repo, scheduleMinute(19, 9).Add(30*time.Second))
	matching := createScheduledAutomation(t, repo, true, scheduleTrigger("ten", "10 19 * * *"))
	// Restart at 19:10 skips the unevaluated interval without executing 19:00 or
	// the in-progress restart minute.
	if state := initializeSchedule(t, repo, scheduleMinute(19, 10)); !state.HighWaterMinute.Equal(
		scheduleMinute(19, 10),
	) {
		t.Fatalf("restart: %+v", state)
	}
	gaps, err := repo.ListAutomationScheduleGaps(t.Context(), AutomationScheduleGapListParams{})
	if err != nil || len(gaps.Items) != 1 {
		t.Fatalf("restart gaps: %+v %v", gaps, err)
	}
	gap := gaps.Items[0]
	if gap.Reason != AutomationScheduleGapCoreRestart ||
		!gap.FromExclusive.Equal(scheduleMinute(19, 0)) ||
		!gap.ThroughInclusive.Equal(scheduleMinute(19, 10)) ||
		!gap.RecordedAt.Equal(scheduleMinute(19, 10)) {
		t.Fatalf("restart gap: %+v", gap)
	}
	_ = matching
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 10).Add(5*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("restart minute executed: %+v", batch)
	}
	// Backward movement after restart records no gap and replays nothing.
	if state := initializeSchedule(t, repo, scheduleMinute(18, 0)); !state.HighWaterMinute.Equal(
		scheduleMinute(19, 10),
	) {
		t.Fatalf("backward restart: %+v", state)
	}
	if count := countScheduleRows(t, database, "automation_schedule_gaps"); count != 1 {
		t.Fatalf("backward gap: %d", count)
	}
	if batch := evaluateSchedule(t, repo, scheduleMinute(18, 30)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("backward evaluation: %+v", batch)
	}
}

// This test protects delayed-tick tolerance and bounded gaps and fails if a
// same-minute late tick misses or a multi-hour jump invents per-minute work.
func TestAutomationScheduleDelayedTickAndBoundedGap(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	// A delayed tick within the scheduled minute still admits it.
	batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(50*time.Second))
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 || batch.Gap != nil {
		t.Fatalf("delayed tick: %+v", batch)
	}
	// Crossing one minute boundary skips the old minute and records one gap.
	interruptTestAutomation(t, repo, batch.Runs[0].ID)
	batch = evaluateSchedule(t, repo, scheduleMinute(19, 2).Add(5*time.Second))
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 || batch.Gap == nil {
		t.Fatalf("boundary crossing: %+v", batch)
	}
	if !batch.Gap.FromExclusive.Equal(scheduleMinute(19, 0)) ||
		!batch.Gap.ThroughInclusive.Equal(scheduleMinute(19, 1)) ||
		batch.Gap.Reason != AutomationScheduleGapClockOrProcessing {
		t.Fatalf("minute gap: %+v", batch.Gap)
	}
	if !batch.Occurrences[0].ScheduledAt.Equal(scheduleMinute(19, 2)) {
		t.Fatalf("evaluated minute: %+v", batch.Occurrences[0])
	}
	// Jumping many hours records one bounded interval, not thousands of runs.
	interruptTestAutomation(t, repo, batch.Runs[0].ID)
	batch = evaluateSchedule(t, repo, scheduleMinute(23, 0).Add(5*time.Second))
	if batch.Gap == nil || !batch.Gap.FromExclusive.Equal(scheduleMinute(19, 2)) ||
		!batch.Gap.ThroughInclusive.Equal(scheduleMinute(22, 59)) {
		t.Fatalf("bounded gap: %+v", batch.Gap)
	}
	if count := countScheduleRows(t, database, "automation_runs"); count != 3 {
		t.Fatalf("invented runs: %d", count)
	}
	if count := countScheduleRows(t, database, "automation_schedule_gaps"); count != 2 {
		t.Fatalf("gaps: %d", count)
	}
}

// This test protects atomic minute admission and fails if a failed commit
// leaves partial records, advances progress, or duplicates on retry.
func TestAutomationScheduleCommitRollback(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	if _, err := database.ExecContext(
		t.Context(),
		`CREATE TRIGGER fail_scheduled_occurrence BEFORE INSERT ON automation_occurrences BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	); err != nil {
		t.Fatal(err)
	}
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	setAutomationClocks(repo, now)
	if _, err := repo.EvaluateAutomationMinute(t.Context(), now, time.UTC); err == nil {
		t.Fatal("expected occurrence write failure")
	}
	for table, want := range map[string]int{
		"automation_occurrences":   0,
		"automation_runs":          0,
		"automation_schedule_gaps": 0,
	} {
		if count := countScheduleRows(t, database, table); count != want {
			t.Fatalf("partial %s: %d", table, count)
		}
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(18, 59)) {
		t.Fatalf("progress advanced on failure: %s", highWater)
	}
	if _, err := database.ExecContext(t.Context(), `DROP TRIGGER fail_scheduled_occurrence`); err != nil {
		t.Fatal(err)
	}
	batch := evaluateSchedule(t, repo, now)
	if len(batch.Runs) != 1 || len(batch.Occurrences) != 1 {
		t.Fatalf("retry: %+v", batch)
	}
	if batch = evaluateSchedule(t, repo, now); len(batch.Runs) != 0 || len(batch.Occurrences) != 0 {
		t.Fatalf("retry duplicated: %+v", batch)
	}
}

// This test protects the final currency check and fails if a stale ticker
// timestamp commits or if rollover retries spin without bound.
func TestAutomationScheduleFinalClockRollover(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	// The tick timestamp is stale by commit time: roll back and evaluate the
	// fresh minute instead, recording the skipped interval as a gap.
	repo.scheduleNow = func() time.Time { return scheduleMinute(19, 2).Add(30 * time.Second) }
	batch, err := repo.EvaluateAutomationMinute(t.Context(), scheduleMinute(19, 0).Add(20*time.Second), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Occurrences) != 1 || !batch.Occurrences[0].ScheduledAt.Equal(scheduleMinute(19, 2)) ||
		batch.Gap == nil || !batch.Gap.FromExclusive.Equal(scheduleMinute(18, 59)) ||
		!batch.Gap.ThroughInclusive.Equal(scheduleMinute(19, 1)) {
		t.Fatalf("rollover batch: %+v", batch)
	}
	var stale int
	if err = database.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM automation_occurrences WHERE scheduled_at = ?`,
		automationTime(scheduleMinute(19, 0)),
	).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale minute committed: %d", stale)
	}
}

// This test proves the final currency check sits after all writes: the
// pre-write sample still sees the old minute, but the newRunID callback
// advances the clock across the boundary during writes, so the post-write
// final check must roll back the stale minute and commit only the fresh one.
func TestAutomationScheduleStaleDuringWritesRollsBack(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	clockNow := scheduleMinute(19, 0).Add(20 * time.Second)
	repo.scheduleNow = func() time.Time { return clockNow }
	originalNewRunID := repo.newRunID
	advanced := false
	repo.newRunID = func() (AutomationRunID, error) {
		if !advanced {
			advanced = true
			clockNow = scheduleMinute(19, 2).Add(30 * time.Second)
		}
		return originalNewRunID()
	}
	batch, err := repo.EvaluateAutomationMinute(t.Context(), scheduleMinute(19, 0).Add(20*time.Second), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatal("newRunID callback never advanced the clock")
	}
	if len(batch.Occurrences) != 1 || !batch.Occurrences[0].ScheduledAt.Equal(scheduleMinute(19, 2)) ||
		batch.Gap == nil || !batch.Gap.FromExclusive.Equal(scheduleMinute(18, 59)) ||
		!batch.Gap.ThroughInclusive.Equal(scheduleMinute(19, 1)) {
		t.Fatalf("committed batch: %+v", batch)
	}
	var stale int
	if err = database.QueryRowContext(
		t.Context(),
		`SELECT count(*) FROM automation_occurrences WHERE scheduled_at = ?`,
		automationTime(scheduleMinute(19, 0)),
	).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale minute committed during writes: %d", stale)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(19, 2)) {
		t.Fatalf("high water: %s", highWater)
	}
}

func TestAutomationScheduleRolloverRetryBound(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	calls := 0
	repo.scheduleNow = func() time.Time {
		calls++
		return scheduleMinute(19, 0).Add(time.Duration(calls) * time.Minute)
	}
	if _, err := repo.EvaluateAutomationMinute(
		t.Context(),
		scheduleMinute(19, 0).Add(20*time.Second),
		time.UTC,
	); err == nil {
		t.Fatal("expected rollover exhaustion")
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 0 {
		t.Fatalf("exhausted retry committed: %d", count)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(18, 59)) {
		t.Fatalf("progress advanced on exhaustion: %s", highWater)
	}
}

// This test protects unparsable stored expressions and fails if an invalid
// definition dispatches partially instead of failing its minute visibly.
func TestAutomationScheduleInvalidStoredExpression(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	if _, err := database.ExecContext(
		t.Context(),
		`UPDATE automations SET triggers_json = '[{"id":"every","kind":"cron","expression":"not a schedule"}]' WHERE id = ?`,
		string(automation.ID),
	); err != nil {
		t.Fatal(err)
	}
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	setAutomationClocks(repo, now)
	if _, err := repo.EvaluateAutomationMinute(t.Context(), now, time.UTC); err == nil {
		t.Fatal("expected expression failure")
	}
	if count := countScheduleRows(t, database, "automation_runs"); count != 0 {
		t.Fatalf("invalid expression dispatched: %d", count)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(18, 59)) {
		t.Fatalf("progress advanced on expression failure: %s", highWater)
	}
}

func TestAutomationScheduleRequiresInitialization(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	setAutomationClocks(repo, now)
	if _, err := repo.EvaluateAutomationMinute(t.Context(), now, time.UTC); !errors.Is(
		err,
		ErrAutomationUnavailable,
	) {
		t.Fatalf("uninitialized evaluation: %v", err)
	}
}

// This test protects disabled definitions and fails if a disabled write admits
// or an enablement back-triggers its own minute.
func TestAutomationScheduleDisabledAndEnablement(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(t, repo, false, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(20*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("disabled admission: %+v", batch)
	}
	// Enablement mid-minute resets the bound and cannot back-trigger that minute.
	setAutomationClocks(repo, scheduleMinute(19, 0).Add(40*time.Second))
	definition := testAutomationDefinition()
	definition.Enabled = true
	definition.Triggers = []AutomationTrigger{scheduleTrigger("every", "* * * * *")}
	if _, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: definition},
	); err != nil {
		t.Fatal(err)
	}
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(50*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 {
		t.Fatalf("enablement back-trigger: %+v", batch)
	}
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second)); len(batch.Runs) != 1 ||
		len(batch.Occurrences) != 1 {
		t.Fatalf("enabled minute: %+v", batch)
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 1 {
		t.Fatalf("occurrences: %d", count)
	}
}

// This test protects revision-consistent race resolution and fails if a
// schedule edit racing evaluation mixes revisions, snapshots, or steps.
// SQLite serializes the two transactions, so only two orders are possible:
// update-first must filter 19:00 through schedule_not_before, and
// evaluate-first must admit revision 1 only. Both orders are proven
// deterministically against real SQLite; no concurrent gate is used because
// the racy interleaving cannot assert a single admission outcome.
func TestAutomationScheduleEditRace(t *testing.T) {
	t.Parallel()
	t.Run("UpdateFirstSkipsMinute", func(t *testing.T) {
		t.Parallel()
		testAutomationScheduleEditRaceUpdateFirst(t)
	})
	t.Run("EvaluateFirstAdmitsRevisionOne", func(t *testing.T) {
		t.Parallel()
		testAutomationScheduleEditRaceEvaluateFirst(t)
	})
}

func renamedScheduleDefinition() AutomationDefinition {
	definition := testAutomationDefinition()
	definition.Name = "Renamed"
	definition.Enabled = true
	definition.Triggers = []AutomationTrigger{
		scheduleTrigger("second", "* * * * *"),
		scheduleTrigger("first", "* * * * *"),
	}
	definition.Steps[0].Parameters = devices.CommandParameters(`{"value":true}`)
	return definition
}

func assertScheduleBatchConsistent(t *testing.T, batch AutomationScheduleBatch) {
	t.Helper()
	if len(batch.Occurrences) != 1 || len(batch.Runs) != 1 {
		t.Fatalf("race batch: %+v", batch)
	}
	occurrence := batch.Occurrences[0]
	run := batch.Runs[0]
	if occurrence.Revision != run.Snapshot.Revision || occurrence.Name != run.Snapshot.Definition.Name ||
		len(occurrence.MatchedTriggers) != 2 || len(run.MatchedTriggerIDs) != 2 {
		t.Fatalf("mixed revision: %+v %+v", occurrence, run.Snapshot)
	}
	for i, trigger := range occurrence.MatchedTriggers {
		if trigger.ID != run.MatchedTriggerIDs[i] || trigger.ID != run.Snapshot.Definition.Triggers[i].ID {
			t.Fatalf("snapshot order mixed: %+v %+v", occurrence.MatchedTriggers, run.MatchedTriggerIDs)
		}
	}
}

// testAutomationScheduleEditRaceUpdateFirst proves the update-first order:
// a mid-minute whole-definition update resets schedule_not_before to 19:01,
// so evaluating 19:00 afterwards must admit nothing, while the strictly
// later minute admits the reordered revision 2 snapshot.
func testAutomationScheduleEditRaceUpdateFirst(t *testing.T) {
	t.Helper()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(
		t, repo, true,
		scheduleTrigger("first", "* * * * *"),
		scheduleTrigger("second", "* * * * *"),
	)
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	setAutomationClocks(repo, scheduleMinute(19, 0).Add(20*time.Second))
	updated, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: renamedScheduleDefinition()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated revision: %+v", updated)
	}
	if bound := queryScheduleBound(t, database, automation.ID); bound != automationTime(scheduleMinute(19, 1)) {
		t.Fatalf("update bound: %s", bound)
	}
	if batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(20*time.Second)); len(batch.Runs) != 0 ||
		len(batch.Occurrences) != 0 || batch.Gap != nil {
		t.Fatalf("update-first back-triggered write minute: %+v", batch)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(19, 0)) {
		t.Fatalf("high water: %s", highWater)
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 0 {
		t.Fatalf("occurrences: %d", count)
	}
	if count := countScheduleRows(t, database, "automation_runs"); count != 0 {
		t.Fatalf("runs: %d", count)
	}
	// The strictly later minute admits the reordered revision 2 snapshot.
	batch := evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second))
	assertScheduleBatchConsistent(t, batch)
	occurrence := batch.Occurrences[0]
	run := batch.Runs[0]
	if occurrence.Revision != 2 || occurrence.Name != "Renamed" {
		t.Fatalf("later minute revision: %+v", occurrence)
	}
	if occurrence.MatchedTriggers[0].ID != "second" || occurrence.MatchedTriggers[1].ID != "first" {
		t.Fatalf("reorder not reflected: %+v", occurrence.MatchedTriggers)
	}
	if string(run.Snapshot.Definition.Steps[0].Parameters) != `{"value":true}` {
		t.Fatalf("steps mixed across revisions: %+v", run.Snapshot.Definition.Steps[0])
	}
}

// testAutomationScheduleEditRaceEvaluateFirst proves the evaluate-first order:
// evaluating 19:00 before the racing edit admits revision 1 only, history
// keeps that snapshot after the edit, and the later minute admits the
// reordered revision 2 snapshot.
func testAutomationScheduleEditRaceEvaluateFirst(t *testing.T) {
	t.Helper()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	automation := createScheduledAutomation(
		t, repo, true,
		scheduleTrigger("first", "* * * * *"),
		scheduleTrigger("second", "* * * * *"),
	)
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	batch := evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(20*time.Second))
	assertScheduleBatchConsistent(t, batch)
	occurrence := batch.Occurrences[0]
	run := batch.Runs[0]
	if occurrence.Revision != 1 || run.Snapshot.Revision != 1 {
		t.Fatalf("evaluate-first must admit revision 1: %+v %+v", occurrence, run.Snapshot)
	}
	if occurrence.Name != "Evening lights" {
		t.Fatalf("evaluate-first name: %+v", occurrence)
	}
	if occurrence.MatchedTriggers[0].ID != "first" || occurrence.MatchedTriggers[1].ID != "second" {
		t.Fatalf("evaluate-first order: %+v", occurrence.MatchedTriggers)
	}
	if string(run.Snapshot.Definition.Steps[0].Parameters) != `{"value":9007199254740993}` {
		t.Fatalf("evaluate-first steps: %+v", run.Snapshot.Definition.Steps[0])
	}
	// The racing edit still writes at 19:00:20, so it cannot back-trigger 19:00.
	setAutomationClocks(repo, scheduleMinute(19, 0).Add(20*time.Second))
	if _, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: automation.ID, ExpectedRevision: 1, Definition: renamedScheduleDefinition()},
	); err != nil {
		t.Fatal(err)
	}
	if bound := queryScheduleBound(t, database, automation.ID); bound != automationTime(scheduleMinute(19, 1)) {
		t.Fatalf("update bound: %s", bound)
	}
	// History retains the evaluation-time revision 1 snapshot after the edit.
	history, err := repo.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("history: %+v %v", history, err)
	}
	stored := history.Items[0]
	if stored.Revision != 1 || stored.Name != "Evening lights" || len(stored.MatchedTriggers) != 2 ||
		stored.MatchedTriggers[0].ID != "first" || stored.MatchedTriggers[1].ID != "second" {
		t.Fatalf("history rewritten: %+v", stored)
	}
	// Complete the admitted Run so the later minute admits instead of skipping.
	interruptTestAutomation(t, repo, batch.Runs[0].ID)
	later := evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second))
	assertScheduleBatchConsistent(t, later)
	if later.Occurrences[0].Revision != 2 || later.Occurrences[0].Name != "Renamed" {
		t.Fatalf("later minute revision: %+v", later.Occurrences[0])
	}
	if later.Occurrences[0].MatchedTriggers[0].ID != "second" ||
		later.Occurrences[0].MatchedTriggers[1].ID != "first" {
		t.Fatalf("reorder not reflected: %+v", later.Occurrences[0].MatchedTriggers)
	}
	if string(later.Runs[0].Snapshot.Definition.Steps[0].Parameters) != `{"value":true}` {
		t.Fatalf("later steps: %+v", later.Runs[0].Snapshot.Definition.Steps[0])
	}
}

// This test protects history retention and ordering and fails if pruning
// removes live started Occurrences, drops progress, rewrites snapshots, or
// misorders pages.
func TestAutomationScheduleHistoryRetentionAndPages(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	skipped := createScheduledAutomation(
		t, repo, true,
		scheduleTrigger("first", "* * * * *"),
		scheduleTrigger("second", "* * * * *"),
	)
	started := createScheduledAutomation(t, repo, true, scheduleTrigger("solo", "* * * * *"))
	doomed := createScheduledAutomation(t, repo, true, scheduleTrigger("doomed", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	manual := admitTestAutomation(t, repo, skipped.ID, "block")
	now := scheduleMinute(19, 0).Add(20 * time.Second)
	batch := evaluateSchedule(t, repo, now)
	if len(batch.Occurrences) != 3 || len(batch.Runs) != 2 {
		t.Fatalf("setup batch: %+v", batch)
	}
	var doomedRunID AutomationRunID
	for _, run := range batch.Runs {
		if run.Snapshot.AutomationID == doomed.ID {
			doomedRunID = run.ID
		}
	}
	// Complete the doomed Run after the retention cutoff so its history survives
	// the skipped-occurrence prune below, then delete its definition. History
	// must keep evaluation-time snapshots after deletion.
	setAutomationClocks(repo, now.Add(20*time.Second))
	interruptTestAutomation(t, repo, doomedRunID)
	doomedRecord, recordErr := repo.GetAutomation(t.Context(), doomed.ID)
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	if err := repo.DeleteAutomation(t.Context(), doomed.ID, doomedRecord.Revision); err != nil {
		t.Fatal(err)
	}
	// Reorder, re-expression, and rename the skipped definition. History must
	// keep evaluation-time snapshots.
	reordered := testAutomationDefinition()
	reordered.Name = "Reordered"
	reordered.Enabled = true
	reordered.Triggers = []AutomationTrigger{
		scheduleTrigger("second", "*/2 * * * *"),
		scheduleTrigger("first", "* * * * *"),
	}
	if _, err := repo.UpdateAutomation(
		t.Context(),
		AutomationUpdate{ID: skipped.ID, ExpectedRevision: 1, Definition: reordered},
	); err != nil {
		t.Fatal(err)
	}
	history, historyErr := repo.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if historyErr != nil || len(history.Items) != 3 {
		t.Fatalf("history: %+v %v", history, historyErr)
	}
	assertScheduleHistorySnapshots(t, history, skipped, doomed, doomedRunID)
	// Retention is strictly-before-cutoff on evaluation and recording times.
	cutoff := now.UTC()
	assertScheduleSkippedPrune(t, repo, database, cutoff, manual)
	// Pruning a started Run deletes its Occurrence atomically: complete the
	// 19:00 started Run, admit 19:01, then prune everything terminal.
	assertSchedulePruneCascade(t, repo, database, batch, started)
}

func assertScheduleHistorySnapshots(
	t *testing.T,
	history AutomationPage[AutomationOccurrence],
	skipped, doomed AutomationRecord,
	doomedRunID AutomationRunID,
) {
	t.Helper()
	for _, occurrence := range history.Items {
		switch occurrence.AutomationID {
		case skipped.ID:
			if occurrence.Status != AutomationOccurrenceSkipped || len(occurrence.MatchedTriggers) != 2 ||
				occurrence.MatchedTriggers[0].ID != "first" ||
				occurrence.MatchedTriggers[0].Expression != "* * * * *" ||
				occurrence.Name != "Evening lights" {
				t.Fatalf("skipped snapshot rewritten: %+v", occurrence)
			}
		case doomed.ID:
			if occurrence.Status != AutomationOccurrenceStarted || occurrence.RunID == nil ||
				*occurrence.RunID != doomedRunID {
				t.Fatalf("deleted-definition history: %+v", occurrence)
			}
		}
	}
}

func assertScheduleSkippedPrune(
	t *testing.T,
	repo *SQLiteRepository,
	database *sql.DB,
	cutoff time.Time,
	manual AutomationRunRecord,
) {
	t.Helper()
	if count, err := repo.PruneAutomationHistory(t.Context(), cutoff); err != nil || count != 0 {
		t.Fatalf("cutoff equality: %d %v", count, err)
	}
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 3 {
		t.Fatalf("equality pruned: %d", count)
	}
	setAutomationClocks(repo, cutoff)
	interruptTestAutomation(t, repo, manual.ID)
	if count, err := repo.PruneAutomationHistory(t.Context(), cutoff.Add(time.Nanosecond)); err != nil || count != 1 {
		t.Fatalf("skipped prune: %d %v", count, err)
	}
	// The manual Run pruned and the skipped Occurrence pruned by evaluation
	// time, while the started Occurrence survives with its active Run and the
	// deleted-definition Occurrence survives with its retained Run.
	if count := countScheduleRows(t, database, "automation_occurrences"); count != 2 {
		t.Fatalf("skipped occurrence retained: %d", count)
	}
	remaining, err := repo.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || len(remaining.Items) != 2 {
		t.Fatalf("retained occurrences: %+v %v", remaining, err)
	}
	for _, occurrence := range remaining.Items {
		if occurrence.Status != AutomationOccurrenceStarted {
			t.Fatalf("skipped occurrence survived: %+v", occurrence)
		}
	}
}

func assertSchedulePruneCascade(
	t *testing.T,
	repo *SQLiteRepository,
	database *sql.DB,
	batch AutomationScheduleBatch,
	started AutomationRecord,
) {
	t.Helper()
	for _, run := range batch.Runs {
		if run.Snapshot.AutomationID == started.ID {
			interruptTestAutomation(t, repo, run.ID)
		}
	}
	evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second))
	if _, err := repo.PruneAutomationHistory(t.Context(), scheduleMinute(20, 0)); err != nil {
		t.Fatal(err)
	}
	persisted, err := repo.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || len(persisted.Items) != 2 {
		t.Fatalf("pruned occurrences: %+v %v", persisted, err)
	}
	for _, occurrence := range persisted.Items {
		if !occurrence.ScheduledAt.Equal(scheduleMinute(19, 1)) {
			t.Fatalf("stale occurrence survived with pruned run: %+v", occurrence)
		}
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(19, 1)) {
		t.Fatalf("progress pruned: %s", highWater)
	}
}

// This test protects gap retention and fails if gaps prune at the cutoff
// instead of strictly before it.
func TestAutomationScheduleGapRetention(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	initializeSchedule(t, repo, scheduleMinute(19, 0))
	recorded := scheduleMinute(19, 10)
	initializeSchedule(t, repo, recorded)
	if _, err := repo.PruneAutomationHistory(t.Context(), recorded); err != nil {
		t.Fatal(err)
	}
	if count := countScheduleRows(t, database, "automation_schedule_gaps"); count != 1 {
		t.Fatalf("cutoff equality pruned gap: %d", count)
	}
	if _, err := repo.PruneAutomationHistory(t.Context(), recorded.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if count := countScheduleRows(t, database, "automation_schedule_gaps"); count != 0 {
		t.Fatalf("gap retained: %d", count)
	}
	if highWater := queryScheduleHighWater(t, database); highWater != automationTime(scheduleMinute(19, 10)) {
		t.Fatalf("progress pruned: %s", highWater)
	}
}

// This test protects descending history pages and fails on misordered items,
// broken cursors, or missing empty-page guarantees.
func TestAutomationScheduleOrderedPages(t *testing.T) {
	t.Parallel()
	repo, _ := testAutomationRepository(t)
	setAutomationClocks(repo, scheduleMinute(18, 59).Add(30*time.Second))
	first := createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	second := createScheduledAutomation(t, repo, true, scheduleTrigger("every", "* * * * *"))
	initializeSchedule(t, repo, scheduleMinute(18, 59))
	evaluateSchedule(t, repo, scheduleMinute(19, 0).Add(5*time.Second))
	evaluateSchedule(t, repo, scheduleMinute(19, 1).Add(5*time.Second))
	page, err := repo.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{Limit: 1})
	if err != nil || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if !page.Items[0].ScheduledAt.Equal(scheduleMinute(19, 1)) {
		t.Fatalf("page order: %+v", page.Items[0])
	}
	head := page.Items[0]
	page, err = repo.ListAutomationOccurrences(
		t.Context(),
		AutomationOccurrenceListParams{
			BeforeScheduledAt:  &head.ScheduledAt,
			BeforeAutomationID: &head.AutomationID,
			Limit:              3,
		},
	)
	if err != nil || len(page.Items) != 3 || page.HasMore {
		t.Fatalf("continuation: %+v %v", page, err)
	}
	assertSchedulePageOrder(t, page.Items)
	filtered, err := repo.ListAutomationOccurrences(
		t.Context(),
		AutomationOccurrenceListParams{AutomationID: &first.ID, Limit: 1},
	)
	if err != nil || len(filtered.Items) != 1 || !filtered.HasMore {
		t.Fatalf("filtered page: %+v %v", filtered, err)
	}
	position := filtered.Items[0]
	filtered, err = repo.ListAutomationOccurrences(
		t.Context(),
		AutomationOccurrenceListParams{
			AutomationID:       &first.ID,
			BeforeScheduledAt:  &position.ScheduledAt,
			BeforeAutomationID: &position.AutomationID,
		},
	)
	if err != nil || len(filtered.Items) != 1 || filtered.HasMore {
		t.Fatalf("filtered continuation: %+v %v", filtered, err)
	}
	empty, err := repo.ListAutomationOccurrences(
		t.Context(),
		AutomationOccurrenceListParams{AutomationID: &second.ID, Limit: 200},
	)
	if err != nil || empty.Items == nil || len(empty.Items) != 2 || empty.HasMore {
		t.Fatalf("filtered full page: %+v %v", empty, err)
	}
	for _, params := range []AutomationOccurrenceListParams{
		{BeforeScheduledAt: &head.ScheduledAt},
		{BeforeAutomationID: &head.AutomationID},
		{Limit: 201},
	} {
		if _, err = repo.ListAutomationOccurrences(t.Context(), params); !errors.Is(err, ErrInvalidAutomation) {
			t.Fatalf("occurrence cursor misuse accepted: %+v", params)
		}
	}
	assertScheduleGapCursorValidation(t, repo, head)
	gaps, err := repo.ListAutomationScheduleGaps(t.Context(), AutomationScheduleGapListParams{})
	if err != nil || gaps.Items == nil || len(gaps.Items) != 0 || gaps.HasMore {
		t.Fatalf("empty gaps page: %+v %v", gaps, err)
	}
}

func assertSchedulePageOrder(t *testing.T, items []AutomationOccurrence) {
	t.Helper()
	for i := 1; i < len(items); i++ {
		previous, current := items[i-1], items[i]
		if current.ScheduledAt.After(previous.ScheduledAt) ||
			(current.ScheduledAt.Equal(previous.ScheduledAt) && current.AutomationID >= previous.AutomationID) {
			t.Fatalf("misordered page: %+v", items)
		}
	}
}

func assertScheduleGapCursorValidation(t *testing.T, repo *SQLiteRepository, head AutomationOccurrence) {
	t.Helper()
	for _, params := range []AutomationScheduleGapListParams{
		{BeforeRecordedAt: &head.ScheduledAt},
		{Limit: 201},
	} {
		if _, err := repo.ListAutomationScheduleGaps(t.Context(), params); !errors.Is(err, ErrInvalidAutomation) {
			t.Fatalf("gap cursor misuse accepted: %+v", params)
		}
	}
}

func TestAutomationScheduleGapIDValidation(t *testing.T) {
	t.Parallel()
	id, err := NewAutomationScheduleGapID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseAutomationScheduleGapID(id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "asg_not-a-uuid", "arn_01900000-0000-7000-8000-000000000001"} {
		if _, err = ParseAutomationScheduleGapID(bad); !errors.Is(err, ErrInvalidAutomation) {
			t.Fatalf("gap ID accepted: %q", bad)
		}
	}
}
