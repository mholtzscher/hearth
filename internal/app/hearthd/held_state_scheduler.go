package hearthd

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const (
	heldStateScheduleInterval = time.Second
	heldStateBatchLimit       = 100
)

type heldStateProcessor interface {
	ProcessDueHeldStates(context.Context, time.Time, int) (int, error)
	StopAdmission()
}

type heldStateTickSource func() (<-chan time.Time, func())

// startHeldStateScheduling owns the periodic expiry scan until Core shutdown.
func startHeldStateScheduling(
	ctx context.Context,
	logger *slog.Logger,
	processor heldStateProcessor,
	now func() time.Time,
	tickSource heldStateTickSource,
) *lifecycle.WorkerHandle {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if tickSource == nil {
		tickSource = func() (<-chan time.Time, func()) {
			ticker := time.NewTicker(heldStateScheduleInterval)
			return ticker.C, ticker.Stop
		}
	}
	scheduler := heldStateScheduler{
		logger: logger, processor: processor, now: now, tickSource: tickSource,
	}
	return lifecycle.StartWorker(ctx, scheduler.run)
}

type heldStateScheduler struct {
	logger     *slog.Logger
	processor  heldStateProcessor
	now        func() time.Time
	tickSource heldStateTickSource
}

func (scheduler *heldStateScheduler) run(ctx context.Context) error {
	ticks, stop := scheduler.tickSource()
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-ticks:
			if !open {
				return scheduler.fail(ctx, automations.ErrAdmissionUnavailable)
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		for {
			processed, err := scheduler.processor.ProcessDueHeldStates(ctx, scheduler.now().UTC(), heldStateBatchLimit)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return scheduler.fail(ctx, err)
			}
			if processed < heldStateBatchLimit {
				break
			}
		}
	}
}

func (scheduler *heldStateScheduler) fail(ctx context.Context, err error) error {
	scheduler.processor.StopAdmission()
	scheduler.logger.ErrorContext(ctx, "held-state scheduler failed",
		slog.String("event", "core.held_state_scheduler_failed"),
		slog.String("error_code", "held_state_scheduler_failed"),
	)
	return err
}
