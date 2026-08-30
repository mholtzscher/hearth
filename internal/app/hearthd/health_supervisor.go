package hearthd

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const healthEvaluationInterval = time.Second

type healthEvaluationService interface {
	PauseHealthEvaluation()
	ResumeHealthEvaluation(time.Time)
	ExpireAdapterLeases(context.Context, time.Time) error
}

type healthSupervisor struct {
	readiness ReadinessChecker
	health    healthEvaluationService
	logger    *slog.Logger
	now       func() time.Time
	ready     bool
	cancel    context.CancelFunc
	done      chan struct{}
	stopOnce  sync.Once
}

func startHealthSupervisor(
	ctx context.Context,
	readiness ReadinessChecker,
	health healthEvaluationService,
	logger *slog.Logger,
) *healthSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	supervisorContext, cancel := context.WithCancel(ctx) //nolint:gosec // Stop owns and invokes cancel.
	supervisor := &healthSupervisor{
		readiness: readiness, health: health, logger: logger, now: time.Now,
		cancel: cancel, done: make(chan struct{}),
	}
	supervisor.poll(supervisorContext, supervisor.now().UTC())
	go supervisor.run(supervisorContext)
	return supervisor
}

func (supervisor *healthSupervisor) run(ctx context.Context) {
	defer close(supervisor.done)
	ticker := time.NewTicker(healthEvaluationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			supervisor.poll(ctx, now.UTC())
		}
	}
}

func (supervisor *healthSupervisor) poll(ctx context.Context, now time.Time) {
	if supervisor.readiness == nil || supervisor.readiness.Check(ctx) != nil {
		if supervisor.ready {
			supervisor.health.PauseHealthEvaluation()
			supervisor.ready = false
		}
		return
	}
	if !supervisor.ready {
		supervisor.health.ResumeHealthEvaluation(now)
		supervisor.ready = true
	}
	if err := supervisor.health.ExpireAdapterLeases(ctx, now); err != nil {
		supervisor.logger.ErrorContext(ctx, "expire Adapter leases", "error", err)
	}
}

func (supervisor *healthSupervisor) Stop() {
	supervisor.stopOnce.Do(supervisor.cancel)
	<-supervisor.done
}
