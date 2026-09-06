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
	// readinessObserved and readinessReason suppress repeated readiness_changed
	// events; readinessReason holds the last unready reason code, or empty when ready.
	readinessObserved bool
	readinessReason   string
	// leaseExpiryPaused records an outage, including initial unready startup,
	// so expiry after recovery grace emits lease_expiry_resumed once.
	leaseExpiryPaused bool
	cancel            context.CancelFunc
	done              chan struct{}
	stopOnce          sync.Once
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
	// A canceled context races the poll ticker during shutdown: the checker
	// then reports dependency failures (for example sqlite_unavailable) for a
	// process that is stopping, not unhealthy. Skip canceled polls before
	// touching remembered readiness so shutdown stays silent.
	if ctx.Err() != nil {
		return
	}
	checkErr := supervisor.checkReadiness(ctx)
	// The context may be canceled while the checker runs, and a checker may
	// still return nil without observing the cancellation. Re-check before
	// updating remembered readiness or the recovery grace window.
	if ctx.Err() != nil {
		return
	}
	if checkErr != nil {
		supervisor.markNotReady(ctx, readinessFailureReason(checkErr))
		return
	}
	supervisor.markReady(ctx, now)
	if now.Before(supervisor.graceUntil) {
		return
	}
	if supervisor.leaseExpiryPaused {
		supervisor.leaseExpiryPaused = false
		supervisor.logger.InfoContext(
			ctx, "core lease expiry resumed", "event", "core.lease_expiry_resumed",
		)
	}
	if err := supervisor.health.ExpireAdapterLeases(ctx, now); err != nil {
		// Expiry can fail because shutdown canceled the poll context mid-call.
		// Normal cancellation is not an error, so suppress that diagnostic
		// while preserving real expiry failures.
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

func (supervisor *healthSupervisor) checkReadiness(ctx context.Context) error {
	if supervisor.readiness == nil {
		return &readinessCheckError{
			reasonCode: readinessCheckFailedReason, err: errors.New("readiness checker is not configured"),
		}
	}
	return supervisor.readiness.Check(ctx)
}

// markNotReady records an unready evaluation, emitting readiness_changed only
// for the first sample or a changed failure reason.
func (supervisor *healthSupervisor) markNotReady(ctx context.Context, reason string) {
	if !supervisor.readinessObserved || supervisor.readinessReason != reason {
		supervisor.logger.WarnContext(
			ctx,
			"core readiness changed",
			"event",
			"core.readiness_changed",
			"status",
			"not_ready",
			"reason_code",
			reason,
		)
		supervisor.readinessObserved = true
		supervisor.readinessReason = reason
	}
	supervisor.ready = false
	supervisor.leaseExpiryPaused = true
}

// markReady records a ready evaluation, emitting readiness_changed only for
// the first sample or a recovery from unready.
func (supervisor *healthSupervisor) markReady(ctx context.Context, now time.Time) {
	if !supervisor.readinessObserved || supervisor.readinessReason != "" {
		supervisor.logger.InfoContext(
			ctx,
			"core readiness changed",
			"event",
			"core.readiness_changed",
			"status",
			"ready",
			"lease_expiry_grace_ms",
			leaseExpiryRecoveryGrace.Milliseconds(),
		)
		supervisor.readinessObserved = true
		supervisor.readinessReason = ""
	}
	if !supervisor.ready {
		supervisor.ready = true
		supervisor.graceUntil = now.Add(leaseExpiryRecoveryGrace)
	}
}

func (supervisor *healthSupervisor) Stop() {
	supervisor.stopOnce.Do(supervisor.cancel)
	<-supervisor.done
}
