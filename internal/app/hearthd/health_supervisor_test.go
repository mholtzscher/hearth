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

// This test protects readiness transition suppression and fails if the first
// sample is skipped, repeats re-emit, reason changes are silent, grace expiry
// loses its single resumption, or unknown errors leak raw text into reasons.
func TestHealthSupervisorLogsReadinessTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger, recorder := withRecording(slog.LevelDebug)
	readiness := &supervisorReadinessStub{}
	health := &supervisorHealthStub{}
	supervisor := &healthSupervisor{
		readiness: readiness, health: health, logger: logger.With("component", "core"),
	}
	startedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)

	notReady := func(reason string) {
		t.Helper()
		requireLastNotReady(t, recorder, reason)
	}

	// First true sample emits ready with the grace window.
	supervisor.poll(ctx, startedAt)
	requireReadinessChangedCount(t, recorder, 1, "first ready")
	requireLastReady(t, recorder)
	// Repeated ready stays silent and expiry waits for the grace boundary.
	supervisor.poll(ctx, startedAt.Add(time.Second))
	requireReadinessChangedCount(t, recorder, 1, "repeated ready")
	if len(health.expires) != 0 {
		t.Fatalf("grace expiry calls = %#v", health.expires)
	}

	// First false sample emits and pauses expiry; repeats stay silent.
	readiness.err = &readinessCheckError{
		reasonCode: "sqlite_unavailable", err: errors.New("SQLite is unavailable"),
	}
	outageAt := startedAt.Add(2 * time.Second)
	supervisor.poll(ctx, outageAt)
	notReady("sqlite_unavailable")
	supervisor.poll(ctx, outageAt.Add(time.Second))
	requireReadinessChangedCount(t, recorder, 2, "repeated failure")
	// Changed failure reason emits once.
	readiness.err = &readinessCheckError{
		reasonCode: "nats_disconnected", err: errors.New("NATS is disconnected"),
	}
	supervisor.poll(ctx, outageAt.Add(2*time.Second))
	notReady("nats_disconnected")
	// Unknown checker errors map to readiness_check_failed without string matching.
	readiness.err = errors.New("token=secret payload")
	supervisor.poll(ctx, outageAt.Add(3*time.Second))
	notReady("readiness_check_failed")
	if len(health.expires) != 0 {
		t.Fatalf("unready expiry calls = %#v", health.expires)
	}

	// Recovery emits ready once with the grace window, then stays silent.
	readiness.err = nil
	recoveredAt := outageAt.Add(4 * time.Second)
	supervisor.poll(ctx, recoveredAt)
	requireReadinessChangedCount(t, recorder, 5, "recovery")
	requireLastReadyStatus(t, recorder)
	supervisor.poll(ctx, recoveredAt.Add(time.Second))
	requireReadinessChangedCount(t, recorder, 5, "repeated recovery")
	if len(health.expires) != 0 {
		t.Fatalf("recovery grace expiry calls = %#v", health.expires)
	}

	// Grace expiry resumes lease expiry with exactly one resumption event.
	recoveryBoundary := recoveredAt.Add(leaseExpiryRecoveryGrace)
	supervisor.poll(ctx, recoveryBoundary)
	if len(health.expires) != 1 || !health.expires[0].Equal(recoveryBoundary) {
		t.Fatalf("recovery boundary expiry calls = %#v", health.expires)
	}
	resumed := recordsWithEvent(recorder.snapshot(), "core.lease_expiry_resumed")
	if len(resumed) != 1 {
		t.Fatalf("lease_expiry_resumed records = %#v, want exactly one", resumed)
	}
	supervisor.poll(ctx, recoveryBoundary.Add(time.Second))
	if got := len(recordsWithEvent(recorder.snapshot(), "core.lease_expiry_resumed")); got != 1 {
		t.Fatalf("lease_expiry_resumed records = %d, want 1 after steady ready", got)
	}
	if len(health.expires) != 2 {
		t.Fatalf("steady ready expiry calls = %#v", health.expires)
	}

	// A second outage and recovery repeats the single resumption.
	readiness.err = errors.New("NATS is disconnected")
	supervisor.poll(ctx, recoveryBoundary.Add(2*time.Second))
	notReady("readiness_check_failed")
	readiness.err = nil
	secondRecovery := recoveryBoundary.Add(3 * time.Second)
	supervisor.poll(ctx, secondRecovery)
	supervisor.poll(ctx, secondRecovery.Add(leaseExpiryRecoveryGrace))
	if got := len(recordsWithEvent(recorder.snapshot(), "core.lease_expiry_resumed")); got != 2 {
		t.Fatalf("lease_expiry_resumed records = %d, want 2 after second recovery", got)
	}
}

// This test protects the initial-outage path and fails if recovery grace
// expires without reporting that lease-expiry polling has resumed.
func TestHealthSupervisorInitialOutageEmitsResumption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger, recorder := withRecording(slog.LevelDebug)
	readiness := &supervisorReadinessStub{err: errors.New("SQLite is unavailable")}
	health := &supervisorHealthStub{}
	supervisor := &healthSupervisor{
		readiness: readiness, health: health, logger: logger.With("component", "core"),
	}
	startedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)

	supervisor.poll(ctx, startedAt)
	if got := len(recordsWithEvent(recorder.snapshot(), "core.readiness_changed")); got != 1 {
		t.Fatalf("readiness_changed records = %d, want 1 for initial outage", got)
	}
	readiness.err = nil
	supervisor.poll(ctx, startedAt.Add(time.Second))
	supervisor.poll(ctx, startedAt.Add(time.Second).Add(leaseExpiryRecoveryGrace))
	if len(health.expires) != 1 {
		t.Fatalf("expiry calls = %#v, want one post-grace expiry", health.expires)
	}
	supervisor.poll(ctx, startedAt.Add(2*time.Second).Add(leaseExpiryRecoveryGrace))
	if resumed := recordsWithEvent(recorder.snapshot(), "core.lease_expiry_resumed"); len(resumed) != 1 {
		t.Fatalf("lease_expiry_resumed records = %#v, want exactly one after initial outage", resumed)
	}
}

func requireReadinessChangedCount(t *testing.T, recorder *recordingHandler, want int, where string) {
	t.Helper()
	if got := len(recordsWithEvent(recorder.snapshot(), "core.readiness_changed")); got != want {
		t.Fatalf("readiness_changed records = %d, want %d at %s", got, want, where)
	}
}

func requireLastNotReady(t *testing.T, recorder *recordingHandler, reason string) {
	t.Helper()
	matching := recordsWithEvent(recorder.snapshot(), "core.readiness_changed")
	if len(matching) == 0 {
		t.Fatalf("missing core.readiness_changed for reason %q", reason)
	}
	last := matching[len(matching)-1]
	requireRecordAttr(t, last, "status", "not_ready")
	requireRecordAttr(t, last, "reason_code", reason)
	if last.Level != slog.LevelWarn {
		t.Fatalf("not-ready readiness_changed level = %v, want Warn", last.Level)
	}
}

func requireLastReady(t *testing.T, recorder *recordingHandler) {
	t.Helper()
	matching := recordsWithEvent(recorder.snapshot(), "core.readiness_changed")
	if len(matching) == 0 {
		t.Fatal("missing core.readiness_changed for first ready")
	}
	first := matching[0]
	requireRecordAttr(t, first, "status", "ready")
	requireRecordAttr(t, first, "component", "core")
	if first.Level != slog.LevelInfo {
		t.Fatalf("ready readiness_changed level = %v, want Info", first.Level)
	}
	if grace, ok := recordAttr(first, "lease_expiry_grace_ms"); !ok || grace.Int64() != 15000 {
		t.Fatalf("ready readiness_changed grace = %#v, want 15000", first)
	}
}

func requireLastReadyStatus(t *testing.T, recorder *recordingHandler) {
	t.Helper()
	matching := recordsWithEvent(recorder.snapshot(), "core.readiness_changed")
	if len(matching) == 0 {
		t.Fatal("missing core.readiness_changed for recovery")
	}
	requireRecordAttr(t, matching[len(matching)-1], "status", "ready")
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
