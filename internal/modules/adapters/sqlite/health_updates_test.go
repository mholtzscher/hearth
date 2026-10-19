package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestSnapshotSynchronizationBootstrapsLegacyAndRetainsMetadata(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	// Legacy baseline has no runtime and must not invent a history transition.
	mustExec(
		t,
		database,
		`INSERT INTO adapter_instances (adapter_id, health_status, health_reason_code, health_source, health_since, health_evidence_at) VALUES ('legacy', 'unknown', 'hearth.awaiting_health', 'core', ?, ?)`,
		at.Format(time.RFC3339Nano),
		at.Format(time.RFC3339Nano),
	)
	if err := repository.QueueCurrentHealthSnapshots(context.Background(), at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	baseline := pending(t, repository)
	if baseline.Snapshot.AdapterID != "legacy" || baseline.Snapshot.Revision != 1 ||
		baseline.Snapshot.RuntimeID != nil ||
		!baseline.Snapshot.ChangedAt.Equal(at) {
		t.Fatalf("legacy baseline = %#v", baseline)
	}
	if countRows(t, database, "health_transitions") != 0 {
		t.Fatal("bootstrap fabricated history")
	}
	if err := repository.QueueCurrentHealthSnapshots(context.Background(), at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	first := deletePending(t, repository)
	second := deletePending(t, repository)
	firstBody, err := adapters.EncodeHealthSnapshot(first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := adapters.EncodeHealthSnapshot(second.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || string(firstBody) != string(secondBody) || !first.EnqueuedAt.Before(second.EnqueuedAt) {
		t.Fatalf("snapshot resynchronization changed semantics: %#v / %#v", first, second)
	}

	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	span := trace.NewSpanContext(
		trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled},
	)
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	const correlation = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	causation := "clm_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	ctx = adapters.WithHealthPublicationMetadata(ctx, correlation, &causation)
	if err = repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	retained := pending(t, repository)
	if retained.CorrelationID != correlation || retained.CausationID == nil || *retained.CausationID != causation ||
		retained.TraceParent != "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01" {
		t.Fatalf("lost publication metadata: %#v", retained)
	}
	repeated := pending(t, repository)
	if repeated.ID != retained.ID || repeated.TraceParent != retained.TraceParent ||
		!repeated.EnqueuedAt.Equal(retained.EnqueuedAt) {
		t.Fatal("reading pending work restamped evidence")
	}
}

func TestCorruptPublicationEvidenceRemainsPendingAndHeadsCannotBeRestamped(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	original := pending(t, repository)
	mustExec(t, database, `UPDATE adapter_health_outbox SET snapshot_json = '{}'`)
	if updates, err := repository.ListPendingHealthUpdates(
		ctx, 64,
	); !errors.Is(err, adapters.ErrCorruptHealthEvidence) ||
		len(updates) != 0 {
		t.Fatalf("corrupt pending row: count=%d err=%v", len(updates), err)
	}
	if countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatal("corrupt row was dropped")
	}
	body, err := adapters.EncodeHealthSnapshot(original.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `UPDATE adapter_health_outbox SET snapshot_json = ?, correlation_id = 'bad'`, string(body))
	if _, err = repository.ListPendingHealthUpdates(ctx, 64); !errors.Is(err, adapters.ErrCorruptHealthEvidence) {
		t.Fatalf("corrupt metadata = %v", err)
	}
	mustExec(t, database, `UPDATE adapter_health_heads SET snapshot_json = '{}'`)
	if err = repository.QueueCurrentHealthSnapshots(
		ctx,
		at.Add(time.Second),
	); !errors.Is(
		err,
		adapters.ErrCorruptHealthEvidence,
	) {
		t.Fatalf("corrupt head synchronization = %v", err)
	}
	if countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatal("corrupt head queued a fabricated update")
	}
	if err = repository.DeleteHealthUpdate(ctx, "bad"); err == nil {
		t.Fatal("accepted invalid delete ID")
	}
	if countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatal("invalid delete removed work")
	}
	if err = repository.DeleteHealthUpdate(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	if err = repository.DeleteHealthUpdate(ctx, original.ID); err != nil {
		t.Fatalf("valid absent delete = %v", err)
	}
	if updates, readErr := repository.ListPendingHealthUpdates(ctx, 64); readErr != nil || len(updates) != 0 {
		t.Fatalf("empty outbox: %v %v", updates, readErr)
	}
}

func TestHealthPublicationBatchPreservesPrefixBeforeCorruptRow(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := t.Context()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []time.Duration{time.Second, 2 * time.Second} {
		if err := repository.QueueCurrentHealthSnapshots(ctx, at.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repository.ListPendingHealthUpdates(ctx, 64)
	if err != nil || len(all) != 3 {
		t.Fatalf("pending batch = %v, %v", all, err)
	}
	bounded, err := repository.ListPendingHealthUpdates(ctx, 1)
	if err != nil || len(bounded) != 1 || bounded[0].ID != all[0].ID ||
		!all[0].EnqueuedAt.Before(all[1].EnqueuedAt) || !all[1].EnqueuedAt.Before(all[2].EnqueuedAt) {
		t.Fatalf("batch limit or enqueue order changed: %v, %v", bounded, err)
	}
	if _, err = repository.ListPendingHealthUpdates(ctx, 0); err == nil {
		t.Fatal("accepted invalid batch limit")
	}
	mustExec(
		t,
		database,
		`UPDATE adapter_health_outbox SET correlation_id = 'bad' WHERE message_id = ?`,
		string(all[1].ID),
	)
	prefix, err := repository.ListPendingHealthUpdates(ctx, 64)
	if !errors.Is(err, adapters.ErrCorruptHealthEvidence) || len(prefix) != 1 || prefix[0].ID != all[0].ID {
		t.Fatalf("corrupt batch lost its valid prefix: %v, %v", prefix, err)
	}
	if countRows(t, database, "adapter_health_outbox") != 3 {
		t.Fatal("read discarded pending evidence")
	}
	if err = repository.DeleteHealthUpdate(ctx, prefix[0].ID); err != nil {
		t.Fatal(err)
	}
	prefix, err = repository.ListPendingHealthUpdates(ctx, 64)
	if !errors.Is(err, adapters.ErrCorruptHealthEvidence) || len(prefix) != 0 ||
		countRows(t, database, "adapter_health_outbox") != 2 {
		t.Fatalf("corrupt row or successor was skipped: %v, %v", prefix, err)
	}
}

func TestFreelyConstructedRuntimeInputsAreRejectedAtServiceAndRepository(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	service := adapters.NewService(
		adapters.Stores{Runtimes: repository, Reads: repository},
		adapters.Dependencies{Now: func() time.Time { return at }},
	)
	invalidClaim := claimWrite(at)
	invalidClaim.SoftwareName = "not.valid"
	if err := repository.ClaimAdapterRuntime(ctx, invalidClaim); err == nil {
		t.Fatal("repository accepted invalid software")
	}
	if err := service.ClaimAdapterRuntime(
		ctx,
		adapters.ClaimAdapterRuntimeParams{
			AdapterID:       "bridge",
			RuntimeID:       testRuntime,
			SoftwareName:    "not.valid",
			SoftwareVersion: "1",
		},
	); err == nil {
		t.Fatal("service accepted invalid software")
	}
	invalidClaim = claimWrite(at)
	invalidClaim.LeaseExpiresAt = at.Add(14 * time.Second)
	if err := repository.ClaimAdapterRuntime(ctx, invalidClaim); err == nil {
		t.Fatal("repository accepted invalid lease")
	}
	if countRows(t, database, "adapter_instances") != 0 {
		t.Fatal("invalid claim wrote evidence")
	}
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	invalidHeartbeat := heartbeatWrite(at.Add(time.Second))
	invalidHeartbeat.ExternalStatus = adapters.AdapterHealthUnhealthy
	if _, err := repository.RecordAdapterHeartbeat(ctx, invalidHeartbeat); err == nil {
		t.Fatal("repository accepted reasonless unhealthy heartbeat")
	}
	if _, err := service.RecordAdapterHeartbeat(
		ctx,
		adapters.AdapterHeartbeat{
			AdapterID:        "bridge",
			RuntimeID:        testRuntime,
			ExternalStatus:   adapters.AdapterHealthUnhealthy,
			SourceObservedAt: at,
		},
	); err == nil {
		t.Fatal("service accepted reasonless unhealthy heartbeat")
	}
	invalidHeartbeat = heartbeatWrite(at.Add(time.Second))
	invalidHeartbeat.ReceivedAt = time.Time{}
	if _, err := repository.RecordAdapterHeartbeat(ctx, invalidHeartbeat); err == nil {
		t.Fatal("repository accepted zero receive time")
	}
	nonUTC := at.In(time.FixedZone("offset", 3600))
	if err := repository.QueueCurrentHealthSnapshots(ctx, nonUTC); err == nil {
		t.Fatal("accepted non-UTC enqueue time")
	}
	if err := repository.ExpireAdapterLeases(ctx, adapters.ExpireLeasesWrite{}); err == nil {
		t.Fatal("accepted zero expiry time")
	}
	if err := repository.ExpireAdapterLeases(ctx, adapters.ExpireLeasesWrite{ExpiresAt: nonUTC}); err == nil {
		t.Fatal("repository accepted non-UTC expiry time")
	}
	if err := service.ExpireAdapterLeases(ctx, time.Time{}); err == nil {
		t.Fatal("service accepted zero expiry time")
	}
	if err := repository.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime},
	); err == nil {
		t.Fatal("accepted zero release time")
	}
	if countRows(t, database, "health_transitions") != 1 || countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatal("invalid runtime input changed evidence")
	}
}
