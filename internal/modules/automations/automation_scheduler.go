package automations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// automationSchedulerTickInterval is the process timer cadence. Evaluation
// still reasons exclusively in UTC minute boundaries; the one-second cadence
// is an implementation detail, not a claim of exact-second delivery.
const automationSchedulerTickInterval = time.Second

// AutomationServiceOption customizes the automation scheduler loop without
// changing manual admission or execution semantics. Application assembly
// aligns the repository final clock with the loop clock by passing the same
// time source to WithAutomationSchedulerClock and WithSchedulerClock.
type AutomationServiceOption func(*Service)

// WithSchedulerClock overrides the scheduler loop clock used for startup
// progress and tick minutes. Tests share one fake with the repository
// scheduler clock so ticks and the final commit check agree on time.
func WithSchedulerClock(clock func() time.Time) AutomationServiceOption {
	return func(service *Service) {
		if clock != nil {
			service.schedulerClock = clock
		}
	}
}

// WithSchedulerWakeup injects a wakeup channel that fully replaces the
// one-second process timer. Each received value evaluates one tick, so
// application tests drive exact minutes without sleeping for timer delivery.
// The channel must stay open while the scheduler runs; closing it terminates
// the loop and clears scheduler health instead of waking every tick at once.
func WithSchedulerWakeup(wakeup <-chan struct{}) AutomationServiceOption {
	return func(service *Service) {
		service.schedulerWakeup = wakeup
	}
}

// StartAutomationScheduler initializes scheduler progress synchronously and
// starts the evaluation loop while holding the lifecycle mutex, so a
// concurrent Stop can never interleave with initialization: Stop blocks until
// the start either succeeds (and Stop then joins the new loop) or fails (and
// Stop is a no-op). The stop/done channels and the running flag are assigned
// only after successful initialization, so a failed start leaves no goroutine
// and no dangling done channel for a Stop to block on. It returns an
// initialization error before readiness instead of starting the loop, and
// marks scheduler health false. Starting an already-running scheduler fails
// without disturbing the loop.
//
// Lock order is schedulerMu before gate: initialization touches scheduler
// health under gate while holding schedulerMu, and the run loop takes only
// gate, so holding schedulerMu across synchronous initialization cannot
// deadlock with an in-flight tick.
func (service *Service) StartAutomationScheduler(ctx context.Context) error {
	service.schedulerMu.Lock()
	defer service.schedulerMu.Unlock()
	if service.schedulerRunning {
		return fmt.Errorf("%w: scheduler already started", ErrAutomationUnavailable)
	}
	if err := service.initializeAutomationScheduler(ctx); err != nil {
		return err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	service.schedulerStop = stop
	service.schedulerDone = done
	service.schedulerRunning = true
	//nolint:gosec // The loop is process-owned: no request scope exists at
	// wakeup-close time, so its failure diagnostic logs without one.
	go service.runAutomationScheduler(stop, done)
	return nil
}

// initializeAutomationScheduler records the startup minute progress before
// readiness, reports scheduler start and any restart gap, and clears only the
// independent scheduler health flag on success.
func (service *Service) initializeAutomationScheduler(ctx context.Context) error {
	now := service.schedulerClock()
	if service.timezone == nil {
		err := fmt.Errorf("%w: household timezone required", ErrInvalidAutomation)
		service.markAutomationSchedulerFailed(ctx, err, now)
		return err
	}
	state, err := service.repo.InitializeAutomationScheduler(ctx, now, service.timezone)
	if err != nil {
		service.markAutomationSchedulerFailed(ctx, err, now)
		return err
	}
	service.gate.Lock()
	service.schedulerHealthy = true
	service.gate.Unlock()
	service.logger.InfoContext(
		ctx,
		"automation scheduler started",
		slog.String("event", "automation.scheduler_started"),
		slog.String("timezone", state.Timezone),
		slog.String("high_water_minute", automationTime(state.HighWaterMinute)),
	)
	service.logAutomationSchedulerRestartGap(ctx, now)
	return nil
}

// logAutomationSchedulerRestartGap reports the core restart gap recorded by
// initialization, if any. Initialization stores the gap with the startup
// instant, so the latest gap recorded exactly at startup is the restart gap;
// older gaps belong to earlier history and stay silent here.
func (service *Service) logAutomationSchedulerRestartGap(ctx context.Context, now time.Time) {
	gaps, err := service.repo.ListAutomationScheduleGaps(ctx, AutomationScheduleGapListParams{Limit: 1})
	if err != nil || len(gaps.Items) == 0 {
		return
	}
	gap := gaps.Items[0]
	if gap.Reason != AutomationScheduleGapCoreRestart || !gap.RecordedAt.Equal(now.UTC()) {
		return
	}
	service.logAutomationScheduleGap(ctx, &gap)
}

// StopAutomationScheduler closes the loop signal and joins the loop before
// returning, all while holding the lifecycle mutex: concurrent stops serialize
// behind the join so every Stop returns only after the loop has exited, and a
// concurrent Start blocks until the prior loop is gone, so no new loop can
// overlap the old one. The running flag clears only after the join, and the
// stop/done channels are released with it. Shutdown closes execution admission
// first, so ticks racing the stop evaluate nothing; already-registered workers
// drain separately through WaitAutomationRuns. Stopping also clears scheduler
// health under the admission gate, so a stopped scheduler reports not-ready
// until a later start re-initializes successfully. Stopping a scheduler that
// is not running is a no-op.
//
// Joining while holding schedulerMu is safe under the schedulerMu-before-gate
// order: the loop takes only gate, never schedulerMu, so it can always exit.
func (service *Service) StopAutomationScheduler() {
	service.schedulerMu.Lock()
	defer service.schedulerMu.Unlock()
	if !service.schedulerRunning {
		return
	}
	close(service.schedulerStop)
	<-service.schedulerDone
	service.schedulerStop = nil
	service.schedulerDone = nil
	service.schedulerRunning = false
	service.gate.Lock()
	service.schedulerHealthy = false
	service.gate.Unlock()
}

// runAutomationScheduler evaluates one UTC minute per wakeup until Stop joins
// it. A nil channel blocks forever, so production ticks on the one-second
// timer while an injected wakeup fully replaces the timer in tests. A closed
// injected wakeup terminates the loop and clears scheduler health instead of
// waking every tick at once in a busy spin; the scheduler stays marked
// running until Stop, so a restart after a closed wakeup requires Stop first.
func (service *Service) runAutomationScheduler(stop <-chan struct{}, done chan struct{}) {
	defer close(done)
	var timer *time.Ticker
	var timerWakeup <-chan time.Time
	if service.schedulerWakeup == nil {
		timer = time.NewTicker(automationSchedulerTickInterval)
		defer timer.Stop()
		timerWakeup = timer.C
	}
	for {
		select {
		case <-stop:
			return
		case <-timerWakeup:
			service.evaluateAutomationSchedule()
		case _, ok := <-service.schedulerWakeup:
			if !ok {
				service.markAutomationSchedulerFailed(
					context.Background(),
					errors.New("automation scheduler wakeup closed"),
					service.schedulerClock(),
				)
				return
			}
			service.evaluateAutomationSchedule()
		}
	}
}

// evaluateAutomationSchedule processes one scheduler wakeup under the shared
// admission lock, so the minute transaction and worker registration serialize
// with manual admission, next-Step admission, shutdown, and the sticky
// executor fault. Ticks while admission is closed evaluate nothing. Failures
// mark only the independent scheduler health; duplicate-minute no-ops never
// clear an unresolved failure. Committed Runs launch through the same worker
// as manual Runs; a crash before registration leaves interrupted Runs that
// can never replay.
func (service *Service) evaluateAutomationSchedule() {
	service.gate.Lock()
	defer service.gate.Unlock()
	if !service.admissionOpen || service.executorFault {
		return
	}
	// Bound persistence like manual admission. Ticks are process-owned rather
	// than request-scoped, and Stop joins the loop separately.
	evaluationContext, cancel := context.WithTimeout(context.Background(), automationPersistenceTimeout)
	defer cancel()
	now := service.schedulerClock()
	batch, err := service.repo.EvaluateAutomationMinute(evaluationContext, now, service.timezone)
	if err != nil {
		service.schedulerHealthy = false
		service.logger.ErrorContext(
			evaluationContext,
			"automation scheduler evaluation failed",
			append(
				service.automationSchedulerFailureFields(now, err),
				slog.String("event", "automation.scheduler_failed"),
			)...,
		)
		return
	}
	if batch.Evaluated {
		service.schedulerHealthy = true
	}
	if batch.Gap != nil {
		service.logAutomationScheduleGap(evaluationContext, batch.Gap)
	}
	for index := range batch.Occurrences {
		occurrence := &batch.Occurrences[index]
		if occurrence.Status == AutomationOccurrenceSkipped {
			service.logAutomationOccurrenceSkipped(evaluationContext, occurrence)
		}
	}
	for index := range batch.Runs {
		run := &batch.Runs[index]
		if service.workers == 0 {
			service.idle = make(chan struct{})
		}
		service.workers++
		// Pass only owned execution inputs, never the response's mutable Run evidence.
		definition := cloneAutomationDefinition(run.Snapshot.Definition)
		go service.executeAutomationRun(run.ID, definition)
	}
}

// logAutomationScheduleGap records one unevaluated UTC minute interval with
// its safe reason code. Gaps explain scheduler coverage, never the identity
// of missed Automations.
func (service *Service) logAutomationScheduleGap(ctx context.Context, gap *AutomationScheduleGap) {
	service.logger.InfoContext(
		ctx,
		"automation schedule gap recorded",
		slog.String("event", "automation.schedule_gap"),
		slog.String("gap_id", gap.ID),
		slog.String("from_exclusive", automationTime(gap.FromExclusive)),
		slog.String("through_inclusive", automationTime(gap.ThroughInclusive)),
		slog.String("reason", gap.Reason),
		slog.String("timezone", service.timezone.String()),
	)
}

// logAutomationOccurrenceSkipped records one overlap skip with every matching
// Trigger ID in definition order. Full Step parameters never enter diagnostics.
func (service *Service) logAutomationOccurrenceSkipped(ctx context.Context, occurrence *AutomationOccurrence) {
	matched := make([]string, 0, len(occurrence.MatchedTriggers))
	for _, trigger := range occurrence.MatchedTriggers {
		matched = append(matched, string(trigger.ID))
	}
	reason := ""
	if occurrence.SkipReason != nil {
		reason = *occurrence.SkipReason
	}
	service.logger.InfoContext(
		ctx,
		"automation occurrence skipped",
		slog.String("event", "automation.occurrence_skipped"),
		slog.String("automation_id", string(occurrence.AutomationID)),
		slog.String("timezone", occurrence.Timezone),
		slog.String("scheduled_at", automationTime(occurrence.ScheduledAt)),
		slog.Any("matched_trigger_ids", matched),
		slog.String("skip_reason", reason),
	)
}

// markAutomationSchedulerFailed records an independent scheduler persistence
// failure. Execution admission and the sticky executor fault are untouched:
// scheduled evaluation pauses while manual admission stays available.
func (service *Service) markAutomationSchedulerFailed(ctx context.Context, err error, now time.Time) {
	service.gate.Lock()
	service.schedulerHealthy = false
	service.gate.Unlock()
	service.logger.ErrorContext(
		ctx,
		"automation scheduler failed",
		append(
			service.automationSchedulerFailureFields(now, err),
			slog.String("event", "automation.scheduler_failed"),
		)...,
	)
}

// automationSchedulerFailureFields builds the shared scheduler_failed
// diagnostic fields: the UTC minute that failed, the safe error code, and the
// household timezone that interpreted the minute. A failed zoneless startup
// never loaded a zone, so it omits the timezone field rather than logging an
// empty one.
func (service *Service) automationSchedulerFailureFields(now time.Time, err error) []any {
	fields := []any{
		slog.String("minute", automationTime(now.UTC().Truncate(time.Minute))),
		slog.String("error_code", automationSchedulerErrorCode(err)),
	}
	if service.timezone != nil {
		fields = append(fields, slog.String("timezone", service.timezone.String()))
	}
	return fields
}

// automationSchedulerErrorCode maps a scheduler failure to a safe diagnostic
// code without leaking storage internals.
func automationSchedulerErrorCode(err error) string {
	if errors.Is(err, ErrAutomationUnavailable) {
		return "automation_unavailable"
	}
	return "automation_internal"
}

// AutomationSchedulerHealthy reports independent scheduler persistence health.
// It is false after a scheduler storage failure until a later initialization
// or actual minute evaluation succeeds; duplicate-minute no-ops never clear
// an unresolved failure.
func (service *Service) AutomationSchedulerHealthy() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	return service.schedulerHealthy
}

// AutomationSchedulerReady combines scheduler health with the shared execution
// admission gate for application readiness; device readiness stays a separate
// input. A scheduler success clears only the scheduler fault and can never
// reopen a latched executor fault or closed admission.
func (service *Service) AutomationSchedulerReady() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	return service.schedulerHealthy && service.admissionOpen && !service.executorFault
}

// HouseholdTimezone exposes the process household timezone used to interpret
// every cron expression. Definitions and Run snapshots carry the zone name;
// this accessor serves diagnostic responses without another configuration seam.
func (service *Service) HouseholdTimezone() *time.Location {
	return service.timezone
}

// ListAutomationOccurrences lists started and skipped schedule matches in
// descending schedule order for diagnostic history.
func (service *Service) ListAutomationOccurrences(
	ctx context.Context,
	input AutomationOccurrenceListParams,
) (AutomationPage[AutomationOccurrence], error) {
	if input.AutomationID != nil {
		if _, err := ParseAutomationID(string(*input.AutomationID)); err != nil {
			return AutomationPage[AutomationOccurrence]{}, err
		}
	}
	if input.BeforeAutomationID != nil {
		if _, err := ParseAutomationID(string(*input.BeforeAutomationID)); err != nil {
			return AutomationPage[AutomationOccurrence]{}, err
		}
	}
	return service.repo.ListAutomationOccurrences(ctx, input)
}

// ListAutomationScheduleGaps lists unevaluated minute intervals in descending
// recording order for restart and clock diagnostics.
func (service *Service) ListAutomationScheduleGaps(
	ctx context.Context,
	input AutomationScheduleGapListParams,
) (AutomationPage[AutomationScheduleGap], error) {
	if input.BeforeID != nil {
		if _, err := ParseAutomationScheduleGapID(*input.BeforeID); err != nil {
			return AutomationPage[AutomationScheduleGap]{}, err
		}
	}
	return service.repo.ListAutomationScheduleGaps(ctx, input)
}
