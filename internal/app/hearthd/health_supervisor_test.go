package hearthd //nolint:testpackage // Tests exercise package-private lifecycle coordination.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type supervisorReadinessStub struct {
	err error
}

func (readiness *supervisorReadinessStub) Check(context.Context) error {
	return readiness.err
}

type supervisorHealthStub struct {
	expires []time.Time
}

func (health *supervisorHealthStub) ExpireAdapterLeases(_ context.Context, at time.Time) error {
	health.expires = append(health.expires, at)
	return nil
}

func TestHealthSupervisorFollowsReadinessTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	readiness := &supervisorReadinessStub{}
	health := &supervisorHealthStub{}
	supervisor := &healthSupervisor{
		readiness: readiness, health: health, logger: slog.New(slog.DiscardHandler),
	}
	firstReadyAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)

	supervisor.poll(ctx, firstReadyAt)
	supervisor.poll(ctx, firstReadyAt.Add(leaseExpiryRecoveryGrace-time.Second))
	if len(health.expires) != 0 {
		t.Fatalf("initial grace expiry calls = %#v", health.expires)
	}
	firstBoundary := firstReadyAt.Add(leaseExpiryRecoveryGrace)
	supervisor.poll(ctx, firstBoundary)
	if len(health.expires) != 1 || !health.expires[0].Equal(firstBoundary) {
		t.Fatalf("initial boundary expiry calls = %#v", health.expires)
	}

	readiness.err = errors.New("NATS disconnected")
	supervisor.poll(ctx, firstBoundary.Add(time.Second))
	supervisor.poll(ctx, firstBoundary.Add(2*time.Second))
	if len(health.expires) != 1 {
		t.Fatalf("not-ready expiry calls = %#v", health.expires)
	}

	readiness.err = nil
	recoveredAt := firstBoundary.Add(3 * time.Second)
	supervisor.poll(ctx, recoveredAt)
	supervisor.poll(ctx, recoveredAt.Add(leaseExpiryRecoveryGrace-time.Second))
	if len(health.expires) != 1 {
		t.Fatalf("recovery grace expiry calls = %#v", health.expires)
	}
	recoveryBoundary := recoveredAt.Add(leaseExpiryRecoveryGrace)
	supervisor.poll(ctx, recoveryBoundary)
	if len(health.expires) != 2 || !health.expires[1].Equal(recoveryBoundary) {
		t.Fatalf("recovery boundary expiry calls = %#v", health.expires)
	}
}

func TestHealthSupervisorPerformsInitialCheckAndStops(t *testing.T) {
	t.Parallel()
	health := &supervisorHealthStub{}
	ctx, cancel := context.WithCancel(context.Background())
	supervisor := startHealthSupervisor(
		ctx,
		&supervisorReadinessStub{},
		health,
		slog.New(slog.DiscardHandler),
	)
	cancel()
	supervisor.Stop()
	supervisor.Stop()
	if len(health.expires) != 0 {
		t.Fatalf("initial expiry calls = %#v", health.expires)
	}
}
