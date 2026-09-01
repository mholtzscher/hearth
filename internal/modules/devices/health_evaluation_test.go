package devices //nolint:testpackage // Tests exercise package-private lease-expiry state.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type healthRepositoryStub struct {
	claimWrites              []ClaimRuntimeWrite
	heartbeatWrites          []HeartbeatWrite
	releaseWrites            []ReleaseRuntimeWrite
	expiryWrites             []ExpireLeasesWrite
	archiveWrites            []ArchiveAdapterParams
	adapter                  AdapterInstance
	adapterPage              Page[AdapterInstance]
	healthHistoryPage        Page[HealthTransition]
	availabilityHistoryPage  Page[HealthTransition]
	listAdapterCalls         int
	historyCalls             int
	availabilityHistoryCalls int
}

func newHealthRepositoryStub() *healthRepositoryStub {
	return &healthRepositoryStub{}
}

func (repository *healthRepositoryStub) ClaimAdapterRuntime(
	_ context.Context,
	write ClaimRuntimeWrite,
) (RuntimeClaim, error) {
	repository.claimWrites = append(repository.claimWrites, write)
	return runtimeClaim(write.RuntimeID), nil
}

func (repository *healthRepositoryStub) RecordAdapterHeartbeat(
	_ context.Context,
	write HeartbeatWrite,
) (HeartbeatResult, error) {
	repository.heartbeatWrites = append(repository.heartbeatWrites, write)
	return HeartbeatResult{LeaseExpiresAt: write.LeaseExpiresAt}, nil
}

func (repository *healthRepositoryStub) ReleaseAdapterRuntime(
	_ context.Context,
	write ReleaseRuntimeWrite,
) error {
	repository.releaseWrites = append(repository.releaseWrites, write)
	return nil
}

func (repository *healthRepositoryStub) ExpireAdapterLeases(
	_ context.Context,
	write ExpireLeasesWrite,
) error {
	repository.expiryWrites = append(repository.expiryWrites, write)
	return nil
}

func (repository *healthRepositoryStub) ListAdapters(
	context.Context,
	ListAdaptersParams,
) (Page[AdapterInstance], error) {
	repository.listAdapterCalls++
	return repository.adapterPage, nil
}

func (repository *healthRepositoryStub) GetAdapter(context.Context, string) (AdapterInstance, error) {
	return repository.adapter, nil
}

func (repository *healthRepositoryStub) ArchiveAdapter(
	_ context.Context,
	params ArchiveAdapterParams,
) error {
	repository.archiveWrites = append(repository.archiveWrites, params)
	return nil
}

func (repository *healthRepositoryStub) ListAdapterHealthHistory(
	context.Context,
	ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	repository.historyCalls++
	return repository.healthHistoryPage, nil
}

func (repository *healthRepositoryStub) ListEntityAvailabilityHistory(
	context.Context,
	ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	repository.availabilityHistoryCalls++
	return repository.availabilityHistoryPage, nil
}

func TestServiceReadinessPausePreservesReadsAndAllowsRuntimeTraffic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{Now: func() time.Time { return now }})
	service.ResumeAdapterLeaseExpiry(now.Add(-2 * adapterLeaseDuration))
	service.PauseAdapterLeaseExpiry()

	if _, err := service.RecordAdapterHeartbeat(ctx, AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if len(repository.heartbeatWrites) != 1 ||
		!repository.heartbeatWrites[0].LeaseGraceUntil.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("paused heartbeat write = %#v", repository.heartbeatWrites)
	}

	adapter, err := service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthHealthy ||
		!adapter.Health.Since.Equal(repository.adapter.Health.Since) {
		t.Fatalf("paused Adapter read = %#v", adapter)
	}

	if releaseErr := service.ReleaseAdapterRuntime(ctx, "simulator", testRuntimeID); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if len(repository.releaseWrites) != 1 ||
		!repository.releaseWrites[0].LeaseGraceUntil.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("paused release write = %#v", repository.releaseWrites)
	}
}

func TestServiceReadinessGraceDefersLeaseExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resumedAt := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})
	service.ResumeAdapterLeaseExpiry(resumedAt)

	if err := service.ExpireAdapterLeases(ctx, resumedAt.Add(adapterLeaseDuration-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if len(repository.expiryWrites) != 0 {
		t.Fatal("lease expiry ran during recovery grace")
	}
	atBoundary := resumedAt.Add(adapterLeaseDuration)
	if err := service.ExpireAdapterLeases(ctx, atBoundary); err != nil {
		t.Fatal(err)
	}
	if len(repository.expiryWrites) != 1 || !repository.expiryWrites[0].ExpiresAt.Equal(atBoundary) {
		t.Fatalf("lease expiry writes = %#v", repository.expiryWrites)
	}
	_, err := service.RecordAdapterHeartbeat(ctx, AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: atBoundary,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServiceSQLiteRecoveryGraceRevivesExpiredRuntimeAndPreventsTakeover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	claimedAt := time.Date(2026, 8, 29, 11, 30, 0, 0, time.UTC)
	now := claimedAt
	runtimeIDs := []RuntimeID{
		testRuntimeID,
		testSecondRuntime,
		testThirdRuntime,
		"run_01890f47-7a6b-7c4d-8e9f-0123456789ae",
	}
	nextRuntimeID := 0
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{
		Now: func() time.Time { return now },
		NewRuntimeID: func() (RuntimeID, error) {
			runtimeID := runtimeIDs[nextRuntimeID]
			nextRuntimeID++
			return runtimeID, nil
		},
	})
	service.ResumeAdapterLeaseExpiry(claimedAt)
	claim, err := service.ClaimAdapterRuntime(ctx, ClaimAdapterRuntimeParams{
		ClaimID: testClaimID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseClaim, err := service.ClaimAdapterRuntime(ctx, ClaimAdapterRuntimeParams{
		ClaimID: "clm_01890f47-7a6b-7c4d-8e9f-0123456789ae", AdapterID: "release-simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = claimedAt.Add(time.Second)
	initialHeartbeat, err := service.RecordAdapterHeartbeat(ctx, AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: claim.RuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	service.PauseAdapterLeaseExpiry()
	resumedAt := initialHeartbeat.LeaseExpiresAt.Add(time.Minute)
	now = resumedAt
	service.ResumeAdapterLeaseExpiry(resumedAt)
	now = resumedAt.Add(time.Second)
	_, err = service.ClaimAdapterRuntime(ctx, ClaimAdapterRuntimeParams{
		ClaimID: testSecondClaimID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.2.0",
	})
	var activeErr *AdapterActiveError
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(resumedAt.Add(adapterLeaseDuration)) {
		t.Fatalf("competing claim during recovery grace error = %v", err)
	}

	now = resumedAt.Add(2 * time.Second)
	revived, err := service.RecordAdapterHeartbeat(ctx, AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: claim.RuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: now,
	})
	if err != nil {
		t.Fatalf("recovery heartbeat: %v", err)
	}
	if !revived.LeaseExpiresAt.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("recovery heartbeat result = %#v", revived)
	}

	now = resumedAt.Add(3 * time.Second)
	_, err = service.ClaimAdapterRuntime(ctx, ClaimAdapterRuntimeParams{
		ClaimID: testThirdClaimID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.3.0",
	})
	activeErr = nil
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(revived.LeaseExpiresAt) {
		t.Fatalf("competing claim after recovery heartbeat error = %v", err)
	}

	now = resumedAt.Add(4 * time.Second)
	if releaseErr := service.ReleaseAdapterRuntime(
		ctx, "release-simulator", releaseClaim.RuntimeID,
	); releaseErr != nil {
		t.Fatalf("release stale runtime during recovery grace: %v", releaseErr)
	}
	releasedAdapter, err := service.GetAdapter(ctx, "release-simulator")
	if err != nil {
		t.Fatal(err)
	}
	if releasedAdapter.Health == nil || releasedAdapter.Health.Reason == nil ||
		releasedAdapter.Health.Reason.Code != "hearth.stopped" || releasedAdapter.Health.Runtime == nil ||
		releasedAdapter.Health.Runtime.Status != runtimeStatusOffline {
		t.Fatalf("stale Adapter after recovery release = %#v", releasedAdapter)
	}
	if releaseErr := service.ReleaseAdapterRuntime(ctx, "simulator", claim.RuntimeID); releaseErr != nil {
		t.Fatalf("release revived runtime: %v", releaseErr)
	}
	adapter, err := service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthUnhealthy ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != runtimeStatusOffline {
		t.Fatalf("Adapter after recovery release = %#v", adapter)
	}
	assertTableCount(t, database, "adapter_runtimes", 2)
}

func TestServiceSQLiteRecoveryGraceExpiresRuntimeAtBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	claimedAt := time.Date(2026, 8, 29, 11, 45, 0, 0, time.UTC)
	now := claimedAt
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{
		Now:          func() time.Time { return now },
		NewRuntimeID: func() (RuntimeID, error) { return testRuntimeID, nil },
	})
	service.ResumeAdapterLeaseExpiry(claimedAt)
	if _, err := service.ClaimAdapterRuntime(ctx, ClaimAdapterRuntimeParams{
		ClaimID: testClaimID, AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}); err != nil {
		t.Fatal(err)
	}

	service.PauseAdapterLeaseExpiry()
	resumedAt := claimedAt.Add(time.Minute)
	now = resumedAt
	service.ResumeAdapterLeaseExpiry(resumedAt)
	boundary := resumedAt.Add(adapterLeaseDuration)
	if err := service.ExpireAdapterLeases(ctx, boundary.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	now = boundary.Add(-time.Nanosecond)
	adapter, err := service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health == nil || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != runtimeStatusOnline {
		t.Fatalf("Adapter before recovery boundary = %#v", adapter)
	}

	if expiryErr := service.ExpireAdapterLeases(ctx, boundary); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	now = boundary
	adapter, err = service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthUnhealthy ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.heartbeat_expired" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != runtimeStatusOffline {
		t.Fatalf("Adapter at recovery boundary = %#v", adapter)
	}
	assertTableCount(t, database, "health_transitions", 2)
}

func TestServiceValidatesClaimsAndAcceptsSoftwareIndependentAdapterReasons(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	generated := 0
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{
		Now: func() time.Time { return now },
		NewRuntimeID: func() (RuntimeID, error) {
			generated++
			return testRuntimeID, nil
		},
	})
	service.ResumeAdapterLeaseExpiry(now)

	invalidClaim := ClaimAdapterRuntimeParams{
		ClaimID: "not-a-claim", AdapterID: "simulator",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}
	if _, err := service.ClaimAdapterRuntime(ctx, invalidClaim); err == nil {
		t.Fatal("invalid claim was accepted")
	}
	if generated != 0 || len(repository.claimWrites) != 0 {
		t.Fatalf("invalid claim generated %d IDs and made %d writes", generated, len(repository.claimWrites))
	}

	validClaim := invalidClaim
	validClaim.ClaimID = testClaimID
	if _, err := service.ClaimAdapterRuntime(ctx, validClaim); err != nil {
		t.Fatal(err)
	}
	if generated != 1 || len(repository.claimWrites) != 1 ||
		!repository.claimWrites[0].LeaseExpiresAt.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("claim write = %#v", repository.claimWrites)
	}

	heartbeat := AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: now,
		Reason:           &HealthReason{Code: "adapter.some-other-software.connection_lost"},
	}
	if _, err := service.RecordAdapterHeartbeat(ctx, heartbeat); err != nil {
		t.Fatal(err)
	}
	heartbeat.Reason.Code = "mutated"
	if len(repository.heartbeatWrites) != 1 {
		t.Fatalf("heartbeat writes = %d, want 1", len(repository.heartbeatWrites))
	}
	write := repository.heartbeatWrites[0]
	if write.Reason == nil || write.Reason.Code != "adapter.some-other-software.connection_lost" ||
		!write.LeaseExpiresAt.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("heartbeat write = %#v", write)
	}
}

func TestServiceAdapterReadsReturnOwnedCopiesAndValidatePages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	sourceObservedAt := now.Add(-time.Second)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	repository.adapter.Health.Status = AdapterHealthUnhealthy
	repository.adapter.Health.Reason = &HealthReason{Code: "hearth.network_unreachable"}
	repository.adapter.Health.ExternalSystem.Status = AdapterHealthUnhealthy
	repository.adapter.Health.ExternalSystem.Reason = &HealthReason{Code: "hearth.network_unreachable"}
	repository.adapterPage = Page[AdapterInstance]{Items: []AdapterInstance{repository.adapter}, HasMore: true}
	repository.healthHistoryPage = Page[HealthTransition]{Items: []HealthTransition{{
		ReceiveOrder: 4, Status: string(AdapterHealthUnhealthy), Source: "external_system",
		Reason:           &HealthReason{Code: "hearth.network_unreachable"},
		SourceObservedAt: &sourceObservedAt, ObservedAt: now,
	}}, HasMore: true}
	repository.availabilityHistoryPage = Page[HealthTransition]{Items: []HealthTransition{{
		ReceiveOrder: 5, Status: string(EntityAvailabilityUnavailable), Source: "adapter_health",
		Reason:           &HealthReason{Code: "hearth.network_unreachable"},
		SourceObservedAt: &sourceObservedAt, ObservedAt: now,
	}}, HasMore: true}
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{Now: func() time.Time { return now }})

	invalidCursor := "INVALID"
	_, invalidPageErr := service.ListAdapters(ctx, ListAdaptersParams{Limit: 10, AfterID: &invalidCursor})
	if !errors.Is(invalidPageErr, ErrInvalidPage) {
		t.Fatalf("invalid Adapter page error = %v", invalidPageErr)
	}
	if repository.listAdapterCalls != 0 {
		t.Fatal("invalid Adapter page reached repository")
	}

	page, err := service.ListAdapters(ctx, ListAdaptersParams{Limit: 10})
	if err != nil || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("Adapter page = %#v, %v", page, err)
	}
	page.Items[0].Health.Reason.Code = "changed"
	page.Items[0].Health.Runtime.SoftwareName = "changed"
	if repository.adapter.Health.Reason.Code != "hearth.network_unreachable" ||
		repository.adapter.Health.Runtime.SoftwareName != "hearth-simulator" {
		t.Fatal("Adapter list result aliases repository data")
	}

	history, err := service.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || !history.HasMore || len(history.Items) != 1 {
		t.Fatalf("health history = %#v, %v", history, err)
	}
	history.Items[0].Reason.Code = "changed"
	*history.Items[0].SourceObservedAt = now
	if repository.healthHistoryPage.Items[0].Reason.Code != "hearth.network_unreachable" ||
		!repository.healthHistoryPage.Items[0].SourceObservedAt.Equal(sourceObservedAt) {
		t.Fatal("health history result aliases repository data")
	}

	availabilityHistory, err := service.ListEntityAvailabilityHistory(
		ctx,
		ListEntityAvailabilityParams{EntityID: commandTestEntityID, Limit: 10},
	)
	if err != nil || !availabilityHistory.HasMore || len(availabilityHistory.Items) != 1 {
		t.Fatalf("availability history = %#v, %v", availabilityHistory, err)
	}
	availabilityHistory.Items[0].Reason.Code = "changed"
	if repository.availabilityHistoryPage.Items[0].Reason.Code != "hearth.network_unreachable" {
		t.Fatal("availability history result aliases repository data")
	}
	if _, invalidErr := service.ListEntityAvailabilityHistory(ctx, ListEntityAvailabilityParams{
		EntityID: "invalid", Limit: 10,
	}); !errors.Is(invalidErr, ErrInvalidPage) || repository.availabilityHistoryCalls != 1 {
		t.Fatalf("invalid availability history error = %v, calls = %d", invalidErr, repository.availabilityHistoryCalls)
	}

	if archiveErr := service.ArchiveAdapter(ctx, "simulator"); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if len(repository.archiveWrites) != 1 || !repository.archiveWrites[0].ArchivedAt.Equal(now) {
		t.Fatalf("archive writes = %#v", repository.archiveWrites)
	}
}

func healthyAdapterFixture(at time.Time) AdapterInstance {
	lastHeartbeatAt := at.Add(-time.Second)
	return AdapterInstance{ID: "simulator", Health: &AdapterHealth{
		Status: AdapterHealthHealthy, Since: at.Add(-time.Minute), EvidenceAt: at,
		Runtime: &RuntimeEvidence{
			ID: testRuntimeID, Status: runtimeStatusOnline, SoftwareName: "hearth-simulator",
			SoftwareVersion: "0.1.0", ClaimedAt: at.Add(-time.Hour),
			LastHeartbeatAt: &lastHeartbeatAt, LeaseExpiresAt: at.Add(adapterLeaseDuration),
		},
		ExternalSystem: &ExternalSystemEvidence{
			Status: AdapterHealthHealthy, SourceObservedAt: at.Add(-time.Second), EvidenceAt: at,
		},
	}}
}
