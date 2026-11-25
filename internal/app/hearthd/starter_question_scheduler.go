package hearthd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const (
	// starterQuestionStartupDelay gives Adapters time to report availability
	// and State before the first pass reads the household.
	starterQuestionStartupDelay = 2 * time.Minute
	// starterQuestionPassTimeout bounds one pass, including the model call. A
	// timed-out pass keeps the previous questions.
	starterQuestionPassTimeout = 90 * time.Second
	// starterQuestionTimeoutStage is the logged stage for a pass that ran out of time.
	starterQuestionTimeoutStage = "timeout"
)

// StarterQuestionRefresher runs one starter question generation pass.
type StarterQuestionRefresher interface {
	RefreshStarterQuestions(ctx context.Context) error
}

// startStarterQuestionRefresh starts the starter question worker under ctx
// and returns its handle. ctx must stay live until coreShutdown stops the
// worker, which happens before SQLite closes.
func startStarterQuestionRefresh(
	ctx context.Context,
	logger *slog.Logger,
	refresher StarterQuestionRefresher,
	interval time.Duration,
) *lifecycle.WorkerHandle {
	scheduler := newStarterQuestionScheduler(logger, refresher, interval)
	return lifecycle.StartWorker(ctx, scheduler.run)
}

func newStarterQuestionScheduler(
	logger *slog.Logger,
	refresher StarterQuestionRefresher,
	interval time.Duration,
) *starterQuestionScheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &starterQuestionScheduler{
		logger:       logger,
		refresher:    refresher,
		startupDelay: starterQuestionStartupDelay,
		interval:     interval,
		passTimeout:  starterQuestionPassTimeout,
	}
}

// starterQuestionScheduler runs the first pass after a startup delay, then one
// pass per interval until its context is canceled. The agent module runs no
// timer of its own.
type starterQuestionScheduler struct {
	logger       *slog.Logger
	refresher    StarterQuestionRefresher
	startupDelay time.Duration
	interval     time.Duration
	passTimeout  time.Duration
}

// run waits for each pass in turn. The timer restarts after each pass ends, so
// a slow pass delays the next one instead of causing catch-up passes.
func (scheduler *starterQuestionScheduler) run(ctx context.Context) error {
	timer := time.NewTimer(scheduler.startupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		scheduler.runPass(ctx)
		timer.Reset(scheduler.interval)
	}
}

// runPass runs one pass under its own deadline. A failure is logged with a
// fixed stage and never stops the schedule; a pass interrupted by shutdown is
// not a failure.
func (scheduler *starterQuestionScheduler) runPass(ctx context.Context) {
	passContext, cancel := context.WithTimeout(ctx, scheduler.passTimeout)
	defer cancel()
	err := scheduler.refresher.RefreshStarterQuestions(passContext)
	if err == nil || ctx.Err() != nil {
		return
	}
	stage := "unknown"
	if refreshErr, ok := errors.AsType[*agent.StarterQuestionRefreshError](err); ok {
		stage = refreshErr.Stage
	}
	if errors.Is(passContext.Err(), context.DeadlineExceeded) {
		stage = starterQuestionTimeoutStage
	}
	scheduler.logger.WarnContext(
		ctx,
		"starter question refresh failed",
		slog.String("event", "core.starter_questions_refresh_failed"),
		slog.String("error_code", "starter_questions_refresh_failed"),
		slog.String("stage", stage),
	)
}
