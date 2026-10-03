package hearthd

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

type scheduleProcessor interface {
	InitializeSchedules(context.Context, time.Time) error
	ProcessDueSchedules(context.Context) (automations.AdmissionOutcome, error)
	StopAdmission()
}

type scheduleTickSource func() (<-chan time.Time, func())

// startScheduleScheduling persists the activation barrier before starting the
// single calendar worker. Ticker timestamps never enter admission decisions.
func startScheduleScheduling(
	ctx context.Context,
	logger *slog.Logger,
	processor scheduleProcessor,
	now func() time.Time,
	tickSource scheduleTickSource,
) (*lifecycle.WorkerHandle, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if err := processor.InitializeSchedules(ctx, now().UTC()); err != nil {
		return nil, err
	}
	if tickSource == nil {
		tickSource = func() (<-chan time.Time, func()) {
			ticker := time.NewTicker(time.Second)
			return ticker.C, ticker.Stop
		}
	}
	scheduler := scheduleScheduler{logger: logger, processor: processor, tickSource: tickSource}
	return lifecycle.StartWorker(ctx, scheduler.run), nil
}

type scheduleScheduler struct {
	logger     *slog.Logger
	processor  scheduleProcessor
	tickSource scheduleTickSource
}

func (scheduler *scheduleScheduler) run(ctx context.Context) error {
	ticks, stop := scheduler.tickSource()
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-ticks:
			if ctx.Err() != nil {
				return nil
			}
			if !open {
				return scheduler.fail(ctx, automations.ErrAdmissionUnavailable)
			}
		}
		if _, err := scheduler.processor.ProcessDueSchedules(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return scheduler.fail(ctx, err)
		}
	}
}

func (scheduler *scheduleScheduler) fail(ctx context.Context, err error) error {
	scheduler.processor.StopAdmission()
	scheduler.logger.ErrorContext(ctx, "automation schedule worker failed",
		slog.String("event", "core.automation_schedule_failed"),
		slog.String("error_code", "automation_schedule_failed"),
	)
	return err
}
