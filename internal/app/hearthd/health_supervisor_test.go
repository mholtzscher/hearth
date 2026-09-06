package hearthd //nolint:testpackage // Tests exercise package-private lifecycle coordination.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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

type supervisorFailingHealthStub struct {
	expires []time.Time
	err     error
}

func (health *supervisorFailingHealthStub) ExpireAdapterLeases(_ context.Context, at time.Time) error {
	health.expires = append(health.expires, at)
	return health.err
}

// This test protects safe lease-expiry diagnostics and fails if a real expiry
// failure goes silent, carries raw error text, or lacks its fixed event and
// code. A canceled poll under the same stub stays silent.
func TestHealthSupervisorLogsLeaseExpiryFailureWithoutRawError(t *testing.T) {
	t.Parallel()
	steadyAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	logger, recorder := withRecording(slog.LevelDebug)
	health := &supervisorFailingHealthStub{err: errors.New("lease table locked s3cr3t-lease")}
	supervisor := &healthSupervisor{
		readiness: &supervisorReadinessStub{}, health: health, logger: logger,
		ready: true, graceUntil: steadyAt.Add(-time.Second),
	}

	supervisor.poll(context.Background(), steadyAt)
	if len(health.expires) != 1 {
		t.Fatalf("failed expiry calls = %#v, want one attempt", health.expires)
	}
	failed := recordsWithEvent(recorder.snapshot(), "core.lease_expiry_failed")
	if len(failed) != 1 {
		t.Fatalf("lease_expiry_failed records = %d, want 1 for real expiry failure", len(failed))
	}
	if failed[0].Level != slog.LevelError {
		t.Fatalf("lease_expiry_failed level = %v, want Error", failed[0].Level)
	}
	requireRecordAttr(t, failed[0], "error_code", "adapter_lease_expiry_failed")
	if _, ok := recordAttr(failed[0], "error"); ok {
		t.Fatalf("lease_expiry_failed carries raw error field (record = %#v)", failed[0])
	}
	if strings.Contains(failed[0].Message, "s3cr3t-lease") {
		t.Fatalf("lease_expiry_failed message leaks expiry error text: %q", failed[0].Message)
	}
	leaked := false
	failed[0].Attrs(func(attr slog.Attr) bool {
		if strings.Contains(attr.Value.String(), "s3cr3t-lease") {
			leaked = true
			return false
		}
		return true
	})
	if leaked {
		t.Fatalf("lease_expiry_failed attrs leak expiry error text (record = %#v)", failed[0])
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	supervisor.poll(canceled, steadyAt.Add(time.Second))
	if len(health.expires) != 1 {
		t.Fatalf("canceled poll expiry calls = %#v, want none further", health.expires)
	}
	if got := len(recordsWithEvent(recorder.snapshot(), "core.lease_expiry_failed")); got != 1 {
		t.Fatalf("lease_expiry_failed records = %d, want 1 after canceled poll", got)
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

// This test protects silent shutdown polls and fails if a poll entered with
// an already-canceled context runs lease expiry or records readiness.
func TestHealthSupervisorIgnoresCanceledPoll(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	health := &supervisorHealthStub{}
	supervisor := &healthSupervisor{
		readiness: &supervisorReadinessStub{}, health: health, logger: slog.New(slog.DiscardHandler),
	}

	supervisor.poll(ctx, time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC))

	if len(health.expires) != 0 {
		t.Fatalf("canceled poll expiry calls = %#v, want none", health.expires)
	}
	if supervisor.ready {
		t.Fatal("canceled poll marked the supervisor ready")
	}
	if !supervisor.graceUntil.IsZero() {
		t.Fatalf("canceled poll graceUntil = %v, want zero", supervisor.graceUntil)
	}
}
