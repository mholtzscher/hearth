//go:build integration

package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	testRuntimeID     = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	testSecondRuntime = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	testThirdRuntime  = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ad")
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

func TestSQLiteAppliedAdapterHealthMaterializesEffectiveAvailabilityForEveryOwnedEntity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC)
	if err := adapterRepository(repository).ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	registeredAt := claimedAt.Add(time.Second)
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return registeredAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, multiEntityRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := registeredAt.Add(time.Second)
	if _, heartbeatErr := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(testAdapterLeaseDuration),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	applyPendingAdapterHealth(t, repository)
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
	if _, heartbeatErr := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID:        "simulator",
		RuntimeID:        testRuntimeID,
		ExternalStatus:   adapters.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt,
		ReceivedAt:       unhealthyAt,
		LeaseExpiresAt:   unhealthyAt.Add(testAdapterLeaseDuration),
		Reason:           &adapters.HealthReason{Code: "hearth.network_unreachable"},
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	applyPendingAdapterHealth(t, repository)

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
func TestSQLiteAvailabilityBatchRollsBackAndInvalidatesOnProjectedUnhealthy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	claimErr := adapterRepository(repository).ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	applyPendingAdapterHealth(t, repository)
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
	if _, heartbeatErr := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
	}
	applyPendingAdapterHealth(t, repository)
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
	if _, unhealthyErr := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, Reason: &adapters.HealthReason{Code: "hearth.external_system_unavailable"},
		ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	applyPendingAdapterHealth(t, repository)
	view, err = repository.GetEntity(ctx, binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability.Status != devices.EntityAvailabilityUnavailable ||
		view.Availability.Source != "adapter_health" ||
		!view.Availability.Since.Equal(unavailableAt) || !view.Availability.EvidenceAt.Equal(unhealthyAt) ||
		view.Availability.Reason == nil ||
		view.Availability.Reason.Code != "hearth.external_system_unavailable" {
		t.Fatalf("inherited unhealthy availability = %#v", view.Availability)
	}
	assertTableCount(t, database, "entity_availability_current", 0)
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, recoveredErr := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	}); recoveredErr != nil {
		t.Fatal(recoveredErr)
	}
	applyPendingAdapterHealth(t, repository)
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
	if err := adapterRepository(
		repository,
	).ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	service := newTestService(
		repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return claimedAt }},
	)
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, err = adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(testAdapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
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
	if _, err = adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID:        "simulator",
		RuntimeID:        testRuntimeID,
		ExternalStatus:   adapters.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt,
		ReceivedAt:       unhealthyAt,
		LeaseExpiresAt:   unhealthyAt.Add(testAdapterLeaseDuration),
		Reason:           &adapters.HealthReason{Code: "hearth.network_unreachable"},
	}); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, err = adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID:        "simulator",
		RuntimeID:        testRuntimeID,
		ExternalStatus:   adapters.AdapterHealthHealthy,
		SourceObservedAt: recoveredAt,
		ReceivedAt:       recoveredAt,
		LeaseExpiresAt:   recoveredAt.Add(testAdapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
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

func TestRegisteredEntityUsesImmutableProjectionRatherThanUnchangedHeartbeatEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	claimedAt := time.Date(2026, 8, 29, 15, 45, 0, 0, time.UTC)
	if err := adapterRepository(
		repository,
	).ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	heartbeatAt := claimedAt.Add(time.Second)
	sourceAt := claimedAt.Add(500 * time.Millisecond)
	if _, err := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthUnknown,
		SourceObservedAt: sourceAt, ReceivedAt: heartbeatAt, LeaseExpiresAt: heartbeatAt.Add(testAdapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
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
	if !view.Availability.Since.Equal(registeredAt) || !view.Availability.EvidenceAt.Equal(claimedAt) ||
		view.Availability.SourceObservedAt != nil {
		t.Fatalf("registered Entity availability = %#v", view.Availability)
	}
	history, err := repository.ListEntityAvailabilityHistory(ctx, devices.ListEntityAvailabilityParams{
		EntityID: binding.Entities[0].EntityID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].SourceObservedAt != nil ||
		!history.Items[0].ObservedAt.Equal(registeredAt) {
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
	if err := adapterRepository(repository).ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
	healthyAt := claimedAt.Add(time.Second)
	leaseExpiresAt := healthyAt.Add(testAdapterLeaseDuration)
	if _, err := adapterRepository(repository).RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: leaseExpiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	applyPendingAdapterHealth(t, repository)
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
	adapter, err := adapterRepository(repository).GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Status != adapters.AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != adapters.RuntimeStatusOnline {
		t.Fatalf("Adapter after availability report = %#v, %v", adapter, err)
	}
	if expiryErr := adapterRepository(repository).ExpireAdapterLeases(ctx, adapters.ExpireLeasesWrite{
		ExpiresAt: leaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	applyPendingAdapterHealth(t, repository)
	assertTableCount(t, database, "entity_availability_current", 0)
	adapter, err = adapterRepository(repository).GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.heartbeat_expired" || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != adapters.RuntimeStatusOffline {
		t.Fatalf("Adapter after supervisor expiry = %#v, %v", adapter, err)
	}
}

func testClaimWrite(runtimeID adapters.RuntimeID, claimedAt time.Time) adapters.ClaimRuntimeWrite {
	return adapters.ClaimRuntimeWrite{
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
