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
	pauses  int
	resumes []time.Time
	expires []time.Time
}

func (health *supervisorHealthStub) PauseAdapterLeaseExpiry() {
	health.pauses++
}

func (health *supervisorHealthStub) ResumeAdapterLeaseExpiry(at time.Time) {
	health.resumes = append(health.resumes, at)
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
	first := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)

	supervisor.poll(ctx, first)
	supervisor.poll(ctx, first.Add(time.Second))
	if len(health.resumes) != 1 || !health.resumes[0].Equal(first) || len(health.expires) != 2 {
		t.Fatalf("initial ready calls = resumes %#v, expires %#v", health.resumes, health.expires)
	}

	readiness.err = errors.New("NATS disconnected")
	supervisor.poll(ctx, first.Add(2*time.Second))
	supervisor.poll(ctx, first.Add(3*time.Second))
	if health.pauses != 1 || len(health.expires) != 2 {
		t.Fatalf("not-ready calls = pauses %d, expires %#v", health.pauses, health.expires)
	}

	readiness.err = nil
	recoveredAt := first.Add(4 * time.Second)
	supervisor.poll(ctx, recoveredAt)
	if len(health.resumes) != 2 || !health.resumes[1].Equal(recoveredAt) ||
		len(health.expires) != 3 || !health.expires[2].Equal(recoveredAt) {
		t.Fatalf("recovery calls = resumes %#v, expires %#v", health.resumes, health.expires)
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
	if len(health.resumes) != 1 || len(health.expires) != 1 {
		t.Fatalf("initial calls = resumes %#v, expires %#v", health.resumes, health.expires)
	}
}
