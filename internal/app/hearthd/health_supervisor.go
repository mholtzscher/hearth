package hearthd

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	leaseExpiryInterval      = time.Second
	leaseExpiryRecoveryGrace = 15 * time.Second
)

type leaseExpiryService interface {
	ExpireAdapterLeases(context.Context, time.Time) error
}

type healthSupervisor struct {
	readiness  ReadinessChecker
	health     leaseExpiryService
	logger     *slog.Logger
	now        func() time.Time
	ready      bool
	graceUntil time.Time
	cancel     context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
}

func startHealthSupervisor(
	ctx context.Context,
	readiness ReadinessChecker,
	health leaseExpiryService,
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
	ticker := time.NewTicker(leaseExpiryInterval)
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
	// A canceled poll races shutdown: the checker then reports dependency
	// failures for a process that is stopping, not unhealthy. Skip canceled
	// polls so shutdown stays silent.
	if ctx.Err() != nil {
		return
	}
	if supervisor.readiness == nil || supervisor.readiness.Check(ctx) != nil {
		supervisor.ready = false
		return
	}
	if !supervisor.ready {
		supervisor.ready = true
		supervisor.graceUntil = now.Add(leaseExpiryRecoveryGrace)
	}
	if now.Before(supervisor.graceUntil) {
		return
	}
	if err := supervisor.health.ExpireAdapterLeases(ctx, now); err != nil {
		// Shutdown can cancel the poll context mid-expiry. Normal
		// cancellation is not an error, so suppress that diagnostic while
		// preserving real expiry failures.
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		supervisor.logger.ErrorContext(
			ctx,
			"expire Adapter leases",
			"event",
			"core.lease_expiry_failed",
			"error_code",
			"adapter_lease_expiry_failed",
		)
	}
}

func (supervisor *healthSupervisor) Stop() {
	supervisor.stopOnce.Do(supervisor.cancel)
	<-supervisor.done
}
