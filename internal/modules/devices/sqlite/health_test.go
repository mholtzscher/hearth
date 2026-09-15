package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	testRuntimeID     = devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testSecondRuntime = devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	testThirdRuntime  = devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
)

type availabilityTestT interface {
	Helper()
	Fatal(...any)
}

func testAvailabilityWrite(t availabilityTestT, write devices.AvailabilityBatchWrite) devices.AvailabilityBatchWrite {
	t.Helper()
	write.RequestID = newAvailabilityRequestID(t)
	return write
}

func TestSQLiteAdapterClaimIsIdempotentAndFencedUntilSupervisorExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	first := testClaimWrite(testRuntimeID, claimedAt)

	const concurrentClaims = 8
	claimErrors := make(chan error, concurrentClaims)
	var start sync.WaitGroup
	start.Add(1)
	for range concurrentClaims {
		go func() {
			start.Wait()
			claimErrors <- repository.ClaimAdapterRuntime(ctx, first)
		}()
	}
	start.Done()
	for range concurrentClaims {
		if err := <-claimErrors; err != nil {
			t.Fatal(err)
		}
	}
	assertTableCount(t, database, "adapter_runtimes", 1)
	assertTableCount(t, database, "health_transitions", 1)

	conflicting := first
	conflicting.SoftwareVersion = "0.2.0"
	if err := repository.ClaimAdapterRuntime(ctx, conflicting); !errors.Is(err, devices.ErrRuntimeClaimConflict) {
		t.Fatalf("runtime ID reuse with different metadata error = %v", err)
	}

	active := testClaimWrite(testSecondRuntime, claimedAt.Add(time.Second))
	err := repository.ClaimAdapterRuntime(ctx, active)
	var activeErr *devices.AdapterActiveError
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("active claim error = %v", err)
	}

	takeover := testClaimWrite(testSecondRuntime, first.LeaseExpiresAt)
	err = repository.ClaimAdapterRuntime(ctx, takeover)
	activeErr = nil
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("claim before supervisor expiry error = %v", err)
	}
	if expiryErr := repository.ExpireAdapterLeases(ctx, devices.ExpireLeasesWrite{
		ExpiresAt: first.LeaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	err = repository.ClaimAdapterRuntime(ctx, takeover)
	if err != nil {
		t.Fatalf("takeover claim after supervisor expiry: %v", err)
	}
	if retryErr := repository.ClaimAdapterRuntime(ctx, first); !errors.Is(retryErr, devices.ErrRuntimeClaimConflict) {
		t.Fatalf("ended runtime claim retry error = %v", retryErr)
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
	history, err := repository.ListAdapterHealthHistory(ctx, devices.ListAdapterHealthParams{
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
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	first := testClaimWrite(testRuntimeID, claimedAt)
	if err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}

	overdueAt := first.LeaseExpiresAt.Add(time.Second)
	renewed, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: overdueAt, ReceivedAt: overdueAt,
		LeaseExpiresAt: overdueAt.Add(testAdapterLeaseDuration),
	})
	if err != nil || !renewed.LeaseExpiresAt.Equal(overdueAt.Add(testAdapterLeaseDuration)) {
		t.Fatalf("overdue heartbeat = %#v, %v", renewed, err)
	}
	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Status != devices.AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != devices.RuntimeStatusOnline || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(overdueAt) {
		t.Fatalf("Adapter after overdue heartbeat = %#v", adapter)
	}

	second := testClaimWrite(testSecondRuntime, overdueAt.Add(time.Second))
	claimErr := repository.ClaimAdapterRuntime(ctx, second)
	var activeErr *devices.AdapterActiveError
	if !errors.As(claimErr, &activeErr) || !activeErr.RetryAfter.Equal(renewed.LeaseExpiresAt) {
		t.Fatalf("claim after overdue heartbeat error = %v", claimErr)
	}
	releasedAt := renewed.LeaseExpiresAt.Add(time.Second)
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, devices.ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt,
	}); releaseErr != nil {
		t.Fatalf("overdue release: %v", releaseErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != devices.RuntimeStatusOffline {
		t.Fatalf("Adapter after overdue release = %#v, %v", adapter, err)
	}
	assertTableCount(t, database, "health_transitions", 3)
}

func TestSQLiteUnknownHeartbeatReplacesCoreEvidenceWithoutTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 45, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Health.Source != healthSourceCore || claimed.Health.SourceObservedAt != nil {
		t.Fatalf("claimed Adapter health = %#v", claimed.Health)
	}

	heartbeatAt := claimedAt.Add(time.Second)
	sourceObservedAt := heartbeatAt.Add(-time.Second)
	if _, err = repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnknown,
		SourceObservedAt: sourceObservedAt, ReceivedAt: heartbeatAt,
		LeaseExpiresAt: heartbeatAt.Add(testAdapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	unknown, err := repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Health.Source != healthSourceAdapter || unknown.Health.SourceObservedAt == nil ||
		!unknown.Health.SourceObservedAt.Equal(sourceObservedAt) ||
		!unknown.Health.Since.Equal(claimedAt) || !unknown.Health.EvidenceAt.Equal(heartbeatAt) {
		t.Fatalf("unknown heartbeat health = %#v", unknown.Health)
	}
	assertTableCount(t, database, "health_transitions", 1)
}

func TestSQLiteHeartbeatRejectsReturnToUnknownWithTypedError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 12, 55, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	unknownAt := healthyAt.Add(time.Second)
	_, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnknown,
		SourceObservedAt: unknownAt, ReceivedAt: unknownAt, LeaseExpiresAt: unknownAt.Add(time.Hour),
	})
	if !errors.Is(err, devices.ErrInvalidHealthTransition) {
		t.Fatalf("return to unknown error = %v", err)
	}
}

func TestSQLiteHeartbeatRefreshesEvidenceWithoutFabricatingTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}

	healthyAt := claimedAt.Add(time.Second)
	result, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt.Add(-time.Second), ReceivedAt: healthyAt,
		LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	})
	if err != nil {
		t.Fatalf("first healthy heartbeat = %#v, %v", result, err)
	}
	if _, repeatErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt.Add(time.Second),
		LeaseExpiresAt: healthyAt.Add(16 * time.Second),
	}); repeatErr != nil {
		t.Fatal(repeatErr)
	}
	assertTableCount(t, database, "health_transitions", 2)

	unhealthyAt := healthyAt.Add(2 * time.Second)
	unhealthy := devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt.Add(-time.Second),
		Reason:           &devices.HealthReason{Code: "hearth.network_unreachable"},
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
	if adapter.Health.Status != devices.AdapterHealthUnhealthy || adapter.Health.Source != healthSourceAdapter ||
		adapter.Health.SourceObservedAt == nil ||
		!adapter.Health.SourceObservedAt.Equal(unhealthy.SourceObservedAt) ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.network_unreachable" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(unhealthy.ReceivedAt) {
		t.Fatalf("current Adapter = %#v", adapter)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, devices.ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Items[0].Source != healthSourceAdapter || history.Items[0].SourceObservedAt == nil ||
		!history.Items[0].SourceObservedAt.Equal(unhealthyAt.Add(-time.Second)) ||
		history.Items[0].Reason == nil || history.Items[0].Reason.Code != "hearth.network_unreachable" {
		t.Fatalf("historical transition = %#v", history.Items[0])
	}

	recoveredAt := unhealthy.ReceivedAt.Add(time.Second)
	result, err = repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
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
	repository := NewDeviceRepository(database, firstLightCatalog(t))
	claimedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	first := testClaimWrite(testRuntimeID, claimedAt)
	if err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}
	releasedAt := claimedAt.Add(time.Second)
	release := devices.ReleaseRuntimeWrite{AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt}
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
	if adapter.Health.Status != devices.AdapterHealthUnhealthy || adapter.Health.Source != healthSourceCore ||
		adapter.Health.SourceObservedAt != nil || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != "offline" {
		t.Fatalf("released Adapter = %#v", adapter)
	}

	second := testClaimWrite(testSecondRuntime, releasedAt.Add(time.Second))
	if secondClaimErr := repository.ClaimAdapterRuntime(ctx, second); secondClaimErr != nil {
		t.Fatal(secondClaimErr)
	}
	if oldReleaseErr := repository.ReleaseAdapterRuntime(ctx, release); !errors.Is(
		oldReleaseErr, devices.ErrRuntimeFenced,
	) {
		t.Fatalf("old release after takeover = %v", oldReleaseErr)
	}
	if expiryErr := repository.ExpireAdapterLeases(
		ctx,
		devices.ExpireLeasesWrite{ExpiresAt: second.LeaseExpiresAt},
	); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health.Source != healthSourceCore || adapter.Health.SourceObservedAt != nil ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.heartbeat_expired" {
		t.Fatalf("expired Adapter = %#v", adapter)
	}

	history, err := repository.ListAdapterHealthHistory(ctx, devices.ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || len(history.Items) != 4 {
		t.Fatalf("Adapter history = %#v, %v", history, err)
	}
}

func TestSQLiteAdapterHealthMaterializesEffectiveAvailabilityForEveryOwnedEntity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	registeredAt := claimedAt.Add(time.Second)
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return registeredAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, multiEntityRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := registeredAt.Add(time.Second)
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(time.Hour),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	reportedAt := healthyAt.Add(time.Second)
	if _, reportErr := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReportedAt: reportedAt,
		Reports: []devices.EntityAvailabilityReport{
			{
				EntityID: binding.Entities[0].EntityID, Status: devices.EntityAvailabilityAvailable,
				SourceObservedAt: reportedAt,
			},
			{
				EntityID: binding.Entities[1].EntityID, Status: devices.EntityAvailabilityUnavailable,
				SourceObservedAt: reportedAt, Reason: &devices.HealthReason{Code: "hearth.network_unreachable"},
			},
		},
	})); reportErr != nil {
		t.Fatal(reportErr)
	}
	unhealthyAt := reportedAt.Add(time.Second)
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(time.Hour),
		Reason: &devices.HealthReason{Code: "hearth.network_unreachable"},
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}

	for index, wantTransitions := range []int{4, 3} {
		entityID := binding.Entities[index].EntityID
		view, viewErr := repository.GetEntity(ctx, entityID)
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		if view.Availability.Status != devices.EntityAvailabilityUnavailable ||
			view.Availability.Source != availabilitySourceAdapterHealth ||
			view.Availability.Reason == nil ||
			view.Availability.Reason.Code != "hearth.network_unreachable" {
			t.Fatalf("Entity %d availability = %#v", index, view.Availability)
		}
		history, historyErr := repository.ListEntityAvailabilityHistory(ctx, devices.ListEntityAvailabilityParams{
			EntityID: entityID, Limit: 10,
		})
		if historyErr != nil {
			t.Fatal(historyErr)
		}
		if len(history.Items) != wantTransitions {
			t.Fatalf("Entity %d history length = %d, want %d", index, len(history.Items), wantTransitions)
		}
	}
}

//nolint:gocognit,gocyclo,cyclop // One timeline verifies batches, invalidation, and current views.
func TestSQLiteAvailabilityBatchRollsBackAndInvalidatesOnUnhealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return claimedAt }},
	)
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
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	view, err := repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != devices.EntityAvailabilityUnknown || view.Availability.Source != healthSourceCore ||
		view.Availability.Reason == nil || view.Availability.Reason.Code != "hearth.awaiting_entity_report" {
		t.Fatalf("healthy availability without report = %#v", view.Availability)
	}
	available := devices.EntityAvailabilityReport{
		EntityID: binding.Entities[0].EntityID, Status: devices.EntityAvailabilityAvailable,
		SourceObservedAt: healthyAt,
	}
	unknownEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []devices.EntityAvailabilityReport{available, {
			EntityID: unknownEntity, Status: devices.EntityAvailabilityAvailable, SourceObservedAt: healthyAt,
		}},
		ReportedAt: healthyAt.Add(time.Second),
	}))
	if !errors.Is(err, devices.ErrEntityNotFound) {
		t.Fatalf("invalid batch error = %v", err)
	}
	assertTableCount(t, database, "entity_availability_current", 0)

	reportedAt := healthyAt.Add(2 * time.Second)
	if _, reportErr := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []devices.EntityAvailabilityReport{available}, ReportedAt: reportedAt,
	})); reportErr != nil {
		t.Fatal(reportErr)
	}
	assertTableCount(t, database, "entity_availability_current", 1)
	assertTableCount(t, database, "health_transitions", 5)
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Enabled || view.Availability.Status != devices.EntityAvailabilityAvailable ||
		view.Availability.Source != "entity_report" || view.Availability.SourceObservedAt == nil ||
		!view.Availability.SourceObservedAt.Equal(healthyAt) {
		t.Fatalf("reported availability = %#v", view.Availability)
	}
	repeatedAt := reportedAt.Add(time.Second)
	repeated := available
	repeated.SourceObservedAt = healthyAt.Add(time.Second)
	if _, repeatErr := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []devices.EntityAvailabilityReport{repeated}, ReportedAt: repeatedAt,
	})); repeatErr != nil {
		t.Fatal(repeatErr)
	}
	var currentSince, evidenceAt, sourceObservedAt string
	var effectiveTransitions int
	if scanErr := database.QueryRowContext(ctx, `
		SELECT current_since, evidence_at, source_observed_at,
		       (SELECT count(*) FROM health_transitions
		        WHERE resource_kind = 'entity' AND entity_id = ?)
		FROM entity_availability_current WHERE entity_id = ?`,
		binding.Entities[0].EntityID, binding.Entities[0].EntityID,
	).Scan(&currentSince, &evidenceAt, &sourceObservedAt, &effectiveTransitions); scanErr != nil {
		t.Fatal(scanErr)
	}
	if currentSince != formatTime(reportedAt) || evidenceAt != formatTime(repeatedAt) ||
		sourceObservedAt != formatTime(repeated.SourceObservedAt) || effectiveTransitions != 3 {
		t.Fatalf(
			"repeated availability = since %q, evidence %q, source %q, transitions %d",
			currentSince, evidenceAt, sourceObservedAt, effectiveTransitions,
		)
	}

	unavailableAt := repeatedAt.Add(time.Second)
	if _, unavailableErr := repository.ReportEntityAvailability(
		ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
			AdapterID: "simulator", RuntimeID: testRuntimeID,
			Reports: []devices.EntityAvailabilityReport{{
				EntityID: binding.Entities[0].EntityID, Status: devices.EntityAvailabilityUnavailable,
				SourceObservedAt: unavailableAt,
				Reason:           &devices.HealthReason{Code: "hearth.external_system_unavailable"},
			}},
			ReportedAt: unavailableAt,
		})); unavailableErr != nil {
		t.Fatal(unavailableErr)
	}
	unhealthyAt := unavailableAt.Add(time.Second)
	if _, unhealthyErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, Reason: &devices.HealthReason{Code: "hearth.external_system_unavailable"},
		ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != devices.EntityAvailabilityUnavailable ||
		view.Availability.Source != "adapter_health" ||
		!view.Availability.Since.Equal(unhealthyAt) || view.Availability.Reason == nil ||
		view.Availability.Reason.Code != "hearth.external_system_unavailable" {
		t.Fatalf("inherited unhealthy availability = %#v", view.Availability)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, recoveredErr := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	}); recoveredErr != nil {
		t.Fatal(recoveredErr)
	}
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != devices.EntityAvailabilityUnknown || view.Availability.Source != healthSourceCore ||
		view.Availability.Reason == nil || view.Availability.Reason.Code != "hearth.awaiting_entity_report" {
		t.Fatalf("availability after healthy recovery = %#v", view.Availability)
	}
}

func TestSQLiteAvailabilityRetryDoesNotRestoreInvalidatedReport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 30, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return claimedAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, err = repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	write := testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReportedAt: healthyAt.Add(time.Second),
		Reports: []devices.EntityAvailabilityReport{{
			EntityID: binding.Entities[0].EntityID, Status: devices.EntityAvailabilityAvailable,
			SourceObservedAt: healthyAt.Add(time.Second),
		}},
	})
	if _, err = repository.ReportEntityAvailability(ctx, write); err != nil {
		t.Fatal(err)
	}
	unhealthyAt := healthyAt.Add(2 * time.Second)
	if _, err = repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(time.Hour),
		Reason: &devices.HealthReason{Code: "hearth.network_unreachable"},
	}); err != nil {
		t.Fatal(err)
	}
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, err = repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt, LeaseExpiresAt: recoveredAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	reportedAt, retryErr := repository.ReportEntityAvailability(ctx, write)
	if retryErr != nil || !reportedAt.Equal(write.ReportedAt) {
		t.Fatalf("availability retry = %v, %v", reportedAt, retryErr)
	}
	view, err := repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != devices.EntityAvailabilityUnknown ||
		view.Availability.Reason == nil || view.Availability.Reason.Code != "hearth.awaiting_entity_report" {
		t.Fatalf("availability after accepted retry = %#v", view.Availability)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	assertTableCount(t, database, "entity_availability_receipts", 1)
	conflict := write
	conflict.Reports = append([]devices.EntityAvailabilityReport(nil), write.Reports...)
	conflict.Reports[0].SourceObservedAt = conflict.Reports[0].SourceObservedAt.Add(time.Second)
	if _, err = repository.ReportEntityAvailability(ctx, conflict); !errors.Is(
		err, devices.ErrInvalidAvailabilityRequest,
	) {
		t.Fatalf("availability request ID conflict error = %v", err)
	}
}

func TestRegisteredEntityUsesBaselineTimeAndInheritedSourceTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 45, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	heartbeatAt := claimedAt.Add(time.Second)
	sourceAt := claimedAt.Add(500 * time.Millisecond)
	if _, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthUnknown,
		SourceObservedAt: sourceAt, ReceivedAt: heartbeatAt, LeaseExpiresAt: heartbeatAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	registeredAt := heartbeatAt.Add(time.Second)
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return registeredAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	view, err := repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Availability.Since.Equal(registeredAt) || !view.Availability.EvidenceAt.Equal(registeredAt) ||
		view.Availability.SourceObservedAt == nil || !view.Availability.SourceObservedAt.Equal(sourceAt) {
		t.Fatalf("registered Entity availability = %#v", view.Availability)
	}
	history, err := repository.ListEntityAvailabilityHistory(ctx, devices.ListEntityAvailabilityParams{
		EntityID: binding.Entities[0].EntityID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].SourceObservedAt == nil ||
		!history.Items[0].SourceObservedAt.Equal(sourceAt) || !history.Items[0].ObservedAt.Equal(registeredAt) {
		t.Fatalf("registered Entity availability history = %#v", history.Items)
	}
}

func TestSQLiteAvailabilityDoesNotExpireActiveRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	leaseExpiresAt := healthyAt.Add(testAdapterLeaseDuration)
	if _, err := repository.RecordAdapterHeartbeat(ctx, devices.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: devices.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: leaseExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return healthyAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	beforeExpiry := leaseExpiresAt.Add(-time.Second)
	report := devices.EntityAvailabilityReport{
		EntityID: binding.Entities[0].EntityID, Status: devices.EntityAvailabilityAvailable,
		SourceObservedAt: beforeExpiry,
	}
	if _, reportErr := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []devices.EntityAvailabilityReport{report}, ReportedAt: beforeExpiry,
	})); reportErr != nil {
		t.Fatalf("report before lease expiry: %v", reportErr)
	}
	if _, reportErr := repository.ReportEntityAvailability(ctx, testAvailabilityWrite(t, devices.AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []devices.EntityAvailabilityReport{report}, ReportedAt: leaseExpiresAt,
	})); reportErr != nil {
		t.Fatalf("report at lease expiry: %v", reportErr)
	}
	assertTableCount(t, database, "entity_availability_current", 1)
	adapter, err := repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Status != devices.AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != devices.RuntimeStatusOnline {
		t.Fatalf("Adapter after availability report = %#v, %v", adapter, err)
	}
	if expiryErr := repository.ExpireAdapterLeases(ctx, devices.ExpireLeasesWrite{
		ExpiresAt: leaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.heartbeat_expired" || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != devices.RuntimeStatusOffline {
		t.Fatalf("Adapter after supervisor expiry = %#v, %v", adapter, err)
	}
}

func testClaimWrite(runtimeID devices.RuntimeID, claimedAt time.Time) devices.ClaimRuntimeWrite {
	return devices.ClaimRuntimeWrite{
		RuntimeID: runtimeID, AdapterID: "simulator",
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
