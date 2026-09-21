package hearthd

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// HistoryPruner performs one history retention pass using a fixed sweep time.
// The app owns the schedule and passes one UTC instant per pass, so every
// module derives its own retention cutoffs from the same instant.
type HistoryPruner interface {
	// PruneHistory deletes module history strictly older than now minus the
	// module's own retention window. now is Core's sweep time, not insert time;
	// the module keeps its retention policy and safety bounds.
	PruneHistory(ctx context.Context, now time.Time) error
}

// historyPruneTask pairs one module's retention pass with the module name the
// app reports failures under. Tasks run in slice order.
type historyPruneTask struct {
	name   string
	pruner HistoryPruner
}

const (
	// historyPruneInterval spaces passes after the startup pass.
	historyPruneInterval = time.Hour
	// devicesHistoryPruneModule names the devices module in retention logs.
	devicesHistoryPruneModule = "devices"
	// automationsHistoryPruneModule names the automations module in retention logs.
	automationsHistoryPruneModule = "automations"
	// agentHistoryPruneModule names the agent module in retention logs.
	agentHistoryPruneModule = "agent"
)

// newHistoryPruneTasks returns the retention tasks in
// devices-then-automations-then-agent order; the passes share a schedule, not a
// transaction or data dependency.
func newHistoryPruneTasks(
	devicePruner HistoryPruner,
	automationPruner HistoryPruner,
	agentPruner HistoryPruner,
) []historyPruneTask {
	return []historyPruneTask{
		{name: devicesHistoryPruneModule, pruner: devicePruner},
		{name: automationsHistoryPruneModule, pruner: automationPruner},
		{name: agentHistoryPruneModule, pruner: agentPruner},
	}
}

// startHistoryPruning starts the shared retention worker under ctx and returns
// its handle. ctx must stay live until coreShutdown stops the worker, which
// happens before SQLite closes.
func startHistoryPruning(
	ctx context.Context,
	logger *slog.Logger,
	devicePruner HistoryPruner,
	automationPruner HistoryPruner,
	agentPruner HistoryPruner,
) *lifecycle.WorkerHandle {
	scheduler := newHistoryPruneScheduler(
		logger, newHistoryPruneTasks(devicePruner, automationPruner, agentPruner),
	)
	return lifecycle.StartWorker(ctx, scheduler.run)
}

// newHistoryPruneScheduler assembles the retention scheduler with production
// clock and interval defaults. Tests use synctest to drive time and ticks.
func newHistoryPruneScheduler(
	logger *slog.Logger,
	tasks []historyPruneTask,
) *historyPruneScheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &historyPruneScheduler{
		logger:   logger,
		tasks:    tasks,
		interval: historyPruneInterval,
	}
}

// historyPruneScheduler runs one startup retention pass, then hourly passes
// until its context is canceled. It is the only schedule that bounds module
// history; no module runs its own timer.
type historyPruneScheduler struct {
	logger   *slog.Logger
	tasks    []historyPruneTask
	interval time.Duration
}

// run performs the startup pass and then starts the hourly ticker. Running the
// startup pass here, inside the worker, keeps a slow sweep off the readiness
// path. Only one pass runs at a time: while a pass runs the loop is not
// receiving, and the ticker is reset after each pass, so ticks missed by a slow
// pass are skipped instead of replayed as a backlog of catch-up passes.
func (scheduler *historyPruneScheduler) run(ctx context.Context) error {
	scheduler.runPass(ctx, time.Now().UTC())
	if ctx.Err() != nil {
		return nil
	}
	ticker := time.NewTicker(scheduler.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		scheduler.runPass(ctx, time.Now().UTC())
		// Reset starts a full interval from completion and invalidates any old
		// tick, so an overrunning pass never triggers catch-up work.
		ticker.Reset(scheduler.interval)
	}
}

// runPass runs every task once with one shared UTC sweep time, sequentially in
// list order. Cancellation is checked before each task, so shutdown prevents
// later tasks. One module's failure is logged once, never stops the remaining
// modules, and never fails the process; the next pass retries it. A pass
// interrupted by shutdown cancellation is not a failure.
func (scheduler *historyPruneScheduler) runPass(ctx context.Context, sweepTime time.Time) {
	for _, task := range scheduler.tasks {
		if ctx.Err() != nil {
			return
		}
		if err := task.pruner.PruneHistory(ctx, sweepTime); err != nil {
			if ctx.Err() != nil {
				return
			}
			scheduler.logFailure(ctx, task.name)
		}
	}
}

// logFailure records one failed module pass with fixed structured event and
// error codes plus the module name. It never logs the upstream error text or
// payload.
func (scheduler *historyPruneScheduler) logFailure(ctx context.Context, module string) {
	event, errorCode := historyPruneFailureCodes(module)
	scheduler.logger.ErrorContext(
		ctx,
		"history prune failed",
		slog.String("event", event),
		slog.String("error_code", errorCode),
		slog.String("module", module),
	)
}

// historyPruneFailureCodes maps a module name to the whole-literal event and
// error code its failed pass is logged under, so a log record greps straight
// back to this table. Raw upstream errors and payloads are never logged.
func historyPruneFailureCodes(module string) (string, string) {
	switch module {
	case devicesHistoryPruneModule:
		return "core.devices_history_prune_failed", "devices_history_prune_failed"
	case automationsHistoryPruneModule:
		return "core.automation_history_prune_failed", "automation_history_prune_failed"
	case agentHistoryPruneModule:
		return "core.agent_history_prune_failed", "agent_history_prune_failed"
	default:
		return "core.history_prune_failed", "history_prune_failed"
	}
}
