//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestClaimOutboxFailureRollsBackAllEvidence(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	// Failed publication evidence must roll back all source evidence (E2).
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_outbox BEFORE INSERT ON adapter_health_outbox BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	)
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err == nil {
		t.Fatal("claim succeeded despite failed outbox")
	}
	for _, table := range []string{"adapter_instances", "adapter_runtimes", "adapter_health_heads", "adapter_health_outbox", "health_transitions"} {
		if count := countRows(t, database, table); count != 0 {
			t.Fatalf("%s after rollback = %d", table, count)
		}
	}
	mustExec(t, database, `DROP TRIGGER fail_outbox`)
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	if countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatal("independent claim did not persist work")
	}
}

func TestHeartbeatEvidenceDoesNotRestampImmutableHead(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	initial := deletePending(t, repository)
	if initial.Snapshot.Revision != 1 || initial.Snapshot.Status != adapters.AdapterHealthUnknown ||
		initial.Snapshot.Reason.Code != "hearth.awaiting_health" {
		t.Fatalf("initial snapshot = %#v", initial.Snapshot)
	}
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if countRows(t, database, "adapter_health_outbox") != 0 || countRows(t, database, "health_transitions") != 1 {
		t.Fatal("claim retry created publication/history")
	}
	firstHeartbeat := heartbeatWrite(at.Add(2 * time.Second))
	if _, err := repository.RecordAdapterHeartbeat(ctx, firstHeartbeat); err != nil {
		t.Fatal(err)
	}
	healthy := deletePending(t, repository)
	if healthy.Snapshot.Revision != 2 || healthy.Snapshot.Status != adapters.AdapterHealthHealthy {
		t.Fatalf("healthy snapshot = %#v", healthy.Snapshot)
	}
	if _, err := repository.RecordAdapterHeartbeat(ctx, heartbeatWrite(at.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	instance, err := repository.GetAdapter(ctx, "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if !instance.Health.EvidenceAt.Equal(at.Add(3*time.Second)) || !instance.Health.Since.Equal(at.Add(2*time.Second)) {
		t.Fatalf("latest health = %#v", instance.Health)
	}
	if countRows(t, database, "adapter_health_outbox") != 0 {
		t.Fatal("unchanged evidence emitted update")
	}
	if err = repository.QueueCurrentHealthSnapshots(ctx, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	recovered := deletePending(t, repository)
	if recovered.ID == healthy.ID || recovered.Snapshot.Revision != 2 ||
		!recovered.Snapshot.EvidenceAt.Equal(firstHeartbeat.ReceivedAt) ||
		!recovered.EnqueuedAt.Equal(at.Add(4*time.Second)) {
		t.Fatalf("recovered immutable snapshot = %#v", recovered)
	}
	if countRows(t, database, "health_transitions") != 2 {
		t.Fatal("recovery fabricated history")
	}
}

func TestSourceOnlyUnknownHeartbeatDoesNotAdvanceRevision(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	unknown := heartbeatWrite(at.Add(time.Second))
	unknown.ExternalStatus = adapters.AdapterHealthUnknown
	if _, err := repository.RecordAdapterHeartbeat(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	instance, err := repository.GetAdapter(ctx, "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Source != "adapter" || !instance.Health.Since.Equal(at) ||
		!instance.Health.EvidenceAt.Equal(unknown.ReceivedAt) {
		t.Fatalf("source-only heartbeat health = %#v", instance)
	}
	update := pending(t, repository)
	if update.Snapshot.Source != "core" || update.Snapshot.Revision != 1 ||
		countRows(t, database, "health_transitions") != 1 ||
		countRows(t, database, "adapter_health_outbox") != 1 {
		t.Fatalf("source-only heartbeat changed immutable evidence: %#v", update)
	}
}

func TestHealthRevisionOverflowRollsBackRuntimeMutation(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	deletePending(t, repository)
	if _, err := repository.RecordAdapterHeartbeat(ctx, heartbeatWrite(at.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	healthy := deletePending(t, repository)
	if _, err := repository.RecordAdapterHeartbeat(ctx, heartbeatWrite(at.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	// Force overflow after heartbeat writes have begun, proving rollback.
	healthyBody, err := adapters.EncodeHealthSnapshot(healthy.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	overflowBody := strings.Replace(
		string(healthyBody),
		`"revision":2`,
		`"revision":`+strconv.FormatInt(math.MaxInt64, 10),
		1,
	)
	mustExec(
		t,
		database,
		`UPDATE adapter_health_heads SET revision = ?, snapshot_json = ?`,
		int64(math.MaxInt64),
		overflowBody,
	)
	unhealthy := heartbeatWrite(at.Add(5 * time.Second))
	unhealthy.ExternalStatus = adapters.AdapterHealthUnhealthy
	unhealthy.Reason = &adapters.HealthReason{Code: "adapter.bridge.offline"}
	if _, err = repository.RecordAdapterHeartbeat(ctx, unhealthy); !errors.Is(err, adapters.ErrHealthRevisionOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
	instance, err := repository.GetAdapter(ctx, "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != adapters.AdapterHealthHealthy ||
		!instance.Health.EvidenceAt.Equal(at.Add(3*time.Second)) ||
		!instance.Health.Runtime.LeaseExpiresAt.Equal(at.Add(18*time.Second)) {
		t.Fatalf("overflow changed runtime health: %#v", instance)
	}
	if countRows(t, database, "health_transitions") != 2 || countRows(t, database, "adapter_health_outbox") != 0 {
		t.Fatal("overflow left partial evidence")
	}
}

func TestConcurrentRuntimeClaimsEstablishOneRevision(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	var start sync.WaitGroup
	start.Add(1)
	results := make(chan error, 6)
	for range 6 {
		go func() { start.Wait(); results <- repository.ClaimAdapterRuntime(ctx, claimWrite(at)) }()
	}
	start.Done()
	for range 6 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if countRows(t, database, "adapter_health_outbox") != 1 || countRows(t, database, "health_transitions") != 1 {
		t.Fatal("concurrent claim duplicated evidence")
	}
}

func TestFailedHeartbeatPublicationCannotChangeHealthOrEntityEvidence(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordAdapterHeartbeat(ctx, heartbeatWrite(at.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	seedMappedEntityReport(t, database, at)
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_outbox BEFORE INSERT ON adapter_health_outbox BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	)
	unhealthy := heartbeatWrite(at.Add(2 * time.Second))
	unhealthy.ExternalStatus = adapters.AdapterHealthUnhealthy
	unhealthy.Reason = &adapters.HealthReason{Code: "adapter.bridge.offline"}
	if _, err := repository.RecordAdapterHeartbeat(ctx, unhealthy); err == nil {
		t.Fatal("heartbeat committed without publication evidence")
	}
	instance, err := repository.GetAdapter(ctx, "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != adapters.AdapterHealthHealthy ||
		!instance.Health.EvidenceAt.Equal(at.Add(time.Second)) {
		t.Fatalf("failed heartbeat changed health: %#v", instance)
	}
	if countRows(t, database, "health_transitions") != 2 || countRows(t, database, "adapter_health_outbox") != 2 ||
		countRows(t, database, "entity_availability_current") != 1 {
		t.Fatal("failed heartbeat left partial effects")
	}
	mustExec(t, database, `DROP TRIGGER fail_outbox`)
	if _, err = repository.RecordAdapterHeartbeat(ctx, unhealthy); err != nil {
		t.Fatalf("independent heartbeat could not recover: %v", err)
	}
	instance, err = repository.GetAdapter(ctx, "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != adapters.AdapterHealthUnhealthy || countRows(t, database, "health_transitions") != 3 ||
		countRows(t, database, "entity_availability_current") != 1 {
		t.Fatal("health recovery failed or wrote Devices evidence")
	}
}

func TestRuntimeFencingExpiryReplacementAndIdempotentRelease(t *testing.T) {
	t.Parallel()
	database, repository, at := runtimeFixture(t)
	ctx := context.Background()
	if err := repository.ClaimAdapterRuntime(ctx, claimWrite(at)); err != nil {
		t.Fatal(err)
	}
	seedMappedEntityReport(t, database, at)
	replacement := claimWrite(at.Add(16 * time.Second))
	replacement.RuntimeID = replacementRuntime
	if err := repository.ClaimAdapterRuntime(ctx, replacement); !errors.Is(err, adapters.ErrAdapterActive) {
		t.Fatalf("unsupervised takeover = %v", err)
	}
	if _, err := repository.RecordAdapterHeartbeat(ctx, heartbeatWrite(at.Add(16*time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := repository.ExpireAdapterLeases(
		ctx,
		adapters.ExpireLeasesWrite{ExpiresAt: at.Add(30 * time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.ClaimAdapterRuntime(ctx, replacement); !errors.Is(err, adapters.ErrAdapterActive) {
		t.Fatalf("renewed runtime takeover = %v", err)
	}
	if err := repository.ExpireAdapterLeases(
		ctx,
		adapters.ExpireLeasesWrite{ExpiresAt: at.Add(31 * time.Second)},
	); err != nil {
		t.Fatal(err)
	}
	replacement.ClaimedAt = at.Add(32 * time.Second)
	replacement.LeaseExpiresAt = replacement.ClaimedAt.Add(15 * time.Second)
	if err := repository.ClaimAdapterRuntime(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RecordAdapterHeartbeat(
		ctx,
		heartbeatWrite(at.Add(33*time.Second)),
	); !errors.Is(
		err,
		adapters.ErrRuntimeFenced,
	) {
		t.Fatalf("old runtime heartbeat = %v", err)
	}
	if err := repository.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{AdapterID: "bridge", RuntimeID: testRuntime, ReleasedAt: at.Add(33 * time.Second)},
	); !errors.Is(
		err,
		adapters.ErrRuntimeFenced,
	) {
		t.Fatalf("old release = %v", err)
	}
	release := adapters.ReleaseRuntimeWrite{
		AdapterID:  "bridge",
		RuntimeID:  replacementRuntime,
		ReleasedAt: at.Add(34 * time.Second),
	}
	if err := repository.ReleaseAdapterRuntime(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReleaseAdapterRuntime(ctx, release); err != nil {
		t.Fatal(err)
	}
	var revisions []adapters.HealthRevision
	for countRows(t, database, "adapter_health_outbox") > 0 {
		revisions = append(revisions, deletePending(t, repository).Snapshot.Revision)
	}
	if len(revisions) != 5 || revisions[4] != 5 || countRows(t, database, "health_transitions") != 5 {
		t.Fatalf("runtime transition revisions = %v", revisions)
	}
	var entityHistory int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM health_transitions WHERE resource_kind = 'entity'`).
		Scan(&entityHistory); err != nil {
		t.Fatal(err)
	}
	if entityHistory != 0 {
		t.Fatalf("Adapters wrote %d Entity transitions", entityHistory)
	}
	if countRows(t, database, "entity_availability_current") != 1 {
		t.Fatal("Adapters invalidated an Entity report")
	}
}
