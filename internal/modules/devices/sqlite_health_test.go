package devices //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	testClaimID       = "clm_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testSecondClaimID = "clm_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	testThirdClaimID  = "clm_01890f47-7a6b-7c4d-8e9f-0123456789ad"
	testRuntimeID     = RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testSecondRuntime = RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	testThirdRuntime  = RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
)

func TestSQLiteAdapterClaimIsIdempotentAndFencedUntilSupervisorExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	first := testClaimWrite(testClaimID, testRuntimeID, claimedAt)

	const concurrentClaims = 8
	claims := make(chan RuntimeClaim, concurrentClaims)
	claimErrors := make(chan error, concurrentClaims)
	var start sync.WaitGroup
	start.Add(1)
	for range concurrentClaims {
		go func() {
			start.Wait()
			claim, err := repository.ClaimAdapterRuntime(ctx, first)
			claims <- claim
			claimErrors <- err
		}()
	}
	start.Done()
	for range concurrentClaims {
		if err := <-claimErrors; err != nil {
			t.Fatal(err)
		}
		claim := <-claims
		if claim.RuntimeID != testRuntimeID || claim.HeartbeatInterval != 5*time.Second ||
			claim.LeaseDuration != 15*time.Second {
			t.Fatalf("claim = %#v", claim)
		}
	}
	assertTableCount(t, database, "adapter_runtimes", 1)
	assertTableCount(t, database, "health_transitions", 1)

	active := testClaimWrite(testSecondClaimID, testSecondRuntime, claimedAt.Add(time.Second))
	_, err := repository.ClaimAdapterRuntime(ctx, active)
	var activeErr *AdapterActiveError
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("active claim error = %v", err)
	}

	takeover := testClaimWrite(testSecondClaimID, testSecondRuntime, first.LeaseExpiresAt)
	_, err = repository.ClaimAdapterRuntime(ctx, takeover)
	activeErr = nil
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("claim before supervisor expiry error = %v", err)
	}
	if expiryErr := repository.ExpireAdapterLeases(ctx, ExpireLeasesWrite{
		ExpiresAt: first.LeaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	claim, err := repository.ClaimAdapterRuntime(ctx, takeover)
	if err != nil || claim.RuntimeID != testSecondRuntime {
		t.Fatalf("takeover claim after supervisor expiry = %#v, %v", claim, err)
	}
	assertTableCount(t, database, "adapter_runtimes", 2)
	var activeRuntime, endedAt, endReason string
	if scanErr := database.QueryRowContext(ctx, `
		SELECT ai.active_runtime_id, old.ended_at, old.end_reason
		FROM adapter_instances AS ai
		JOIN adapter_runtimes AS old ON old.runtime_id = ?
		WHERE ai.adapter_id = 'simulator'`, testRuntimeID,
	).Scan(&activeRuntime, &endedAt, &endReason); scanErr != nil {
		t.Fatal(scanErr)
	}
	if activeRuntime != string(testSecondRuntime) || endedAt != formatTime(first.LeaseExpiresAt) ||
		endReason != "heartbeat_expired" {
		t.Fatalf("takeover state = %q, %q, %q", activeRuntime, endedAt, endReason)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 3 || history.Items[0].Reason == nil ||
		history.Items[0].Reason.Code != "hearth.awaiting_health" ||
		history.Items[1].Reason == nil || history.Items[1].Reason.Code != "hearth.heartbeat_expired" {
		t.Fatalf("takeover history = %#v", history.Items)
	}
}

func TestSQLiteActiveRuntimeTrafficCanRenewOrReleaseOverdueLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	first := testClaimWrite(testClaimID, testRuntimeID, claimedAt)
	if _, err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}

	overdueAt := first.LeaseExpiresAt.Add(time.Second)
	renewed, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: overdueAt, ReceivedAt: overdueAt,
		LeaseExpiresAt: overdueAt.Add(adapterLeaseDuration),
	})
	if err != nil || !renewed.LeaseExpiresAt.Equal(overdueAt.Add(adapterLeaseDuration)) {
		t.Fatalf("overdue heartbeat = %#v, %v", renewed, err)
	}
	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Status != AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != runtimeStatusOnline || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(overdueAt) {
		t.Fatalf("Adapter after overdue heartbeat = %#v", adapter)
	}

	second := testClaimWrite(testSecondClaimID, testSecondRuntime, overdueAt.Add(time.Second))
	_, claimErr := repository.ClaimAdapterRuntime(ctx, second)
	var activeErr *AdapterActiveError
	if !errors.As(claimErr, &activeErr) || !activeErr.RetryAfter.Equal(renewed.LeaseExpiresAt) {
		t.Fatalf("claim after overdue heartbeat error = %v", claimErr)
	}
	releasedAt := renewed.LeaseExpiresAt.Add(time.Second)
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt,
	}); releaseErr != nil {
		t.Fatalf("overdue release: %v", releaseErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != runtimeStatusOffline {
		t.Fatalf("Adapter after overdue release = %#v, %v", adapter, err)
	}
	assertTableCount(t, database, "health_transitions", 3)
}

func TestSQLiteHeartbeatRefreshesEvidenceWithoutFabricatingTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	_, claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testClaimID, testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}

	healthyAt := claimedAt.Add(time.Second)
	result, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt.Add(-time.Second), ReceivedAt: healthyAt,
		LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	})
	if err != nil {
		t.Fatalf("first healthy heartbeat = %#v, %v", result, err)
	}
	if _, repeatErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt.Add(time.Second),
		LeaseExpiresAt: healthyAt.Add(16 * time.Second),
	}); repeatErr != nil {
		t.Fatal(repeatErr)
	}
	assertTableCount(t, database, "health_transitions", 2)

	unhealthyAt := healthyAt.Add(2 * time.Second)
	unhealthy := HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt.Add(-time.Second),
		Reason:           &HealthReason{Code: "hearth.network_unreachable"},
		ReceivedAt:       unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}
	if _, unhealthyErr := repository.RecordAdapterHeartbeat(ctx, unhealthy); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	unhealthy.SourceObservedAt = unhealthy.SourceObservedAt.Add(time.Second)
	unhealthy.ReceivedAt = unhealthy.ReceivedAt.Add(time.Second)
	unhealthy.LeaseExpiresAt = unhealthy.LeaseExpiresAt.Add(time.Second)
	if _, refreshErr := repository.RecordAdapterHeartbeat(ctx, unhealthy); refreshErr != nil {
		t.Fatal(refreshErr)
	}
	assertTableCount(t, database, "health_transitions", 3)

	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Status != AdapterHealthUnhealthy || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.network_unreachable" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(unhealthy.ReceivedAt) {
		t.Fatalf("current Adapter = %#v", adapter)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Items[0].Reason == nil || history.Items[0].Reason.Code != "hearth.network_unreachable" {
		t.Fatalf("historical transition = %#v", history.Items[0])
	}

	recoveredAt := unhealthy.ReceivedAt.Add(time.Second)
	result, err = repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	})
	if err != nil {
		t.Fatalf("recovery heartbeat = %#v, %v", result, err)
	}
}

func TestSQLiteReleaseAndExpiryPersistOfflineHealth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	first := testClaimWrite(testClaimID, testRuntimeID, claimedAt)
	if _, err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}
	releasedAt := claimedAt.Add(time.Second)
	release := ReleaseRuntimeWrite{AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt}
	if err := repository.ReleaseAdapterRuntime(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := repository.ReleaseAdapterRuntime(ctx, release); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Status != AdapterHealthUnhealthy || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != "offline" {
		t.Fatalf("released Adapter = %#v", adapter)
	}

	second := testClaimWrite(testSecondClaimID, testSecondRuntime, releasedAt.Add(time.Second))
	if _, secondClaimErr := repository.ClaimAdapterRuntime(ctx, second); secondClaimErr != nil {
		t.Fatal(secondClaimErr)
	}
	if oldReleaseErr := repository.ReleaseAdapterRuntime(ctx, release); !errors.Is(oldReleaseErr, ErrRuntimeFenced) {
		t.Fatalf("old release after takeover = %v", oldReleaseErr)
	}
	if expiryErr := repository.ExpireAdapterLeases(
		ctx,
		ExpireLeasesWrite{ExpiresAt: second.LeaseExpiresAt},
	); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.heartbeat_expired" {
		t.Fatalf("expired Adapter = %#v", adapter)
	}

	history, err := repository.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || len(history.Items) != 4 {
		t.Fatalf("Adapter history = %#v, %v", history, err)
	}
}

//nolint:gocognit,gocyclo,cyclop // One timeline verifies batches, invalidation, and current views.
func TestSQLiteAvailabilityBatchRollsBackAndInvalidatesOnUnhealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	_, claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testClaimID, testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	_, disableErr := database.ExecContext(
		ctx,
		"UPDATE entities SET enabled = 0 WHERE id = ?",
		binding.Entities[0].EntityID,
	)
	if disableErr != nil {
		t.Fatal(disableErr)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	view, err := repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != EntityAvailabilityUnknown || view.Availability.Source != healthSourceCore ||
		view.Availability.Reason == nil || view.Availability.Reason.Code != "hearth.awaiting_entity_report" {
		t.Fatalf("healthy availability without report = %#v", view.Availability)
	}
	available := EntityAvailabilityReport{
		EntityID: binding.Entities[0].EntityID, Status: EntityAvailabilityAvailable, SourceObservedAt: healthyAt,
	}
	unknownEntity, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{available, {
			EntityID: unknownEntity, Status: EntityAvailabilityAvailable, SourceObservedAt: healthyAt,
		}},
		ReportedAt: healthyAt.Add(time.Second),
	})
	if !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("invalid batch error = %v", err)
	}
	assertTableCount(t, database, "entity_availability_current", 0)

	reportedAt := healthyAt.Add(2 * time.Second)
	if _, reportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{available}, ReportedAt: reportedAt,
	}); reportErr != nil {
		t.Fatal(reportErr)
	}
	assertTableCount(t, database, "entity_availability_current", 1)
	assertTableCount(t, database, "health_transitions", 4)
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Enabled || view.Availability.Status != EntityAvailabilityAvailable ||
		view.Availability.Source != "entity_report" || view.Availability.SourceObservedAt == nil ||
		!view.Availability.SourceObservedAt.Equal(healthyAt) {
		t.Fatalf("reported availability = %#v", view.Availability)
	}
	repeatedAt := reportedAt.Add(time.Second)
	repeated := available
	repeated.SourceObservedAt = healthyAt.Add(time.Second)
	if _, repeatErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{repeated}, ReportedAt: repeatedAt,
	}); repeatErr != nil {
		t.Fatal(repeatErr)
	}
	var currentSince, evidenceAt, sourceObservedAt string
	var directTransitions int
	if scanErr := database.QueryRowContext(ctx, `
		SELECT current_since, evidence_at, source_observed_at,
		       (SELECT count(*) FROM health_transitions
		        WHERE resource_kind = 'entity' AND entity_id = ?)
		FROM entity_availability_current WHERE entity_id = ?`,
		binding.Entities[0].EntityID, binding.Entities[0].EntityID,
	).Scan(&currentSince, &evidenceAt, &sourceObservedAt, &directTransitions); scanErr != nil {
		t.Fatal(scanErr)
	}
	if currentSince != formatTime(reportedAt) || evidenceAt != formatTime(repeatedAt) ||
		sourceObservedAt != formatTime(repeated.SourceObservedAt) || directTransitions != 2 {
		t.Fatalf(
			"repeated availability = since %q, evidence %q, source %q, transitions %d",
			currentSince, evidenceAt, sourceObservedAt, directTransitions,
		)
	}

	unavailableAt := repeatedAt.Add(time.Second)
	if _, unavailableErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{{
			EntityID: binding.Entities[0].EntityID, Status: EntityAvailabilityUnavailable,
			SourceObservedAt: unavailableAt,
			Reason:           &HealthReason{Code: "hearth.external_system_unavailable"},
		}},
		ReportedAt: unavailableAt,
	}); unavailableErr != nil {
		t.Fatal(unavailableErr)
	}
	unhealthyAt := unavailableAt.Add(time.Second)
	if _, unhealthyErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, Reason: &HealthReason{Code: "hearth.external_system_unavailable"},
		ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != EntityAvailabilityUnavailable || view.Availability.Source != "adapter_health" ||
		!view.Availability.Since.Equal(unhealthyAt) || view.Availability.Reason == nil ||
		view.Availability.Reason.Code != "hearth.external_system_unavailable" {
		t.Fatalf("inherited unhealthy availability = %#v", view.Availability)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, recoveredErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	}); recoveredErr != nil {
		t.Fatal(recoveredErr)
	}
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != EntityAvailabilityUnknown || view.Availability.Source != healthSourceCore ||
		view.Availability.Reason == nil || view.Availability.Reason.Code != "hearth.awaiting_entity_report" {
		t.Fatalf("availability after healthy recovery = %#v", view.Availability)
	}
}

func TestSQLiteAvailabilityDoesNotExpireActiveRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	if _, err := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testClaimID, testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	leaseExpiresAt := healthyAt.Add(adapterLeaseDuration)
	if _, err := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: leaseExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return healthyAt }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	beforeExpiry := leaseExpiresAt.Add(-time.Second)
	report := EntityAvailabilityReport{
		EntityID: binding.Entities[0].EntityID, Status: EntityAvailabilityAvailable,
		SourceObservedAt: beforeExpiry,
	}
	if _, reportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{report}, ReportedAt: beforeExpiry,
	}); reportErr != nil {
		t.Fatalf("report before lease expiry: %v", reportErr)
	}
	if _, reportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{report}, ReportedAt: leaseExpiresAt,
	}); reportErr != nil {
		t.Fatalf("report at lease expiry: %v", reportErr)
	}
	assertTableCount(t, database, "entity_availability_current", 1)
	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Status != AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != runtimeStatusOnline {
		t.Fatalf("Adapter after availability report = %#v, %v", adapter, err)
	}
	if expiryErr := repository.ExpireAdapterLeases(ctx, ExpireLeasesWrite{
		ExpiresAt: leaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.heartbeat_expired" || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != runtimeStatusOffline {
		t.Fatalf("Adapter after supervisor expiry = %#v, %v", adapter, err)
	}
}

func testClaimWrite(claimID string, runtimeID RuntimeID, claimedAt time.Time) ClaimRuntimeWrite {
	return ClaimRuntimeWrite{
		ClaimID: claimID, RuntimeID: runtimeID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		ClaimedAt: claimedAt, LeaseExpiresAt: claimedAt.Add(15 * time.Second),
	}
}

func assertTableCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}
