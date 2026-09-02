package devices //nolint:testpackage // Tests exercise package-private health evaluation.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type healthRepositoryStub struct {
	claimWrites              []ClaimRuntimeWrite
	heartbeatWrites          []HeartbeatWrite
	releaseWrites            []ReleaseRuntimeWrite
	expiryWrites             []ExpireLeasesWrite
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
) error {
	repository.claimWrites = append(repository.claimWrites, write)
	return nil
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

func TestServiceExpireAdapterLeasesValidatesAndNormalizesTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newHealthRepositoryStub()
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{})

	if err := service.ExpireAdapterLeases(ctx, time.Time{}); err == nil {
		t.Fatal("zero lease expiry time was accepted")
	}
	location := time.FixedZone("test", 2*60*60)
	expiresAt := time.Date(2026, 8, 29, 11, 0, 0, 0, location)
	if err := service.ExpireAdapterLeases(ctx, expiresAt); err != nil {
		t.Fatal(err)
	}
	if len(repository.expiryWrites) != 1 ||
		!repository.expiryWrites[0].ExpiresAt.Equal(expiresAt.UTC()) ||
		repository.expiryWrites[0].ExpiresAt.Location() != time.UTC {
		t.Fatalf("lease expiry writes = %#v", repository.expiryWrites)
	}
}

func TestServiceValidatesClaimsAndAcceptsSoftwareIndependentAdapterReasons(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	service := newTestService(repository, nil, firstLightCatalog(t), Dependencies{
		Now: func() time.Time { return now },
	})
	invalidClaim := ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: "not-a-runtime",
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}
	if err := service.ClaimAdapterRuntime(ctx, invalidClaim); err == nil {
		t.Fatal("invalid claim was accepted")
	}
	if len(repository.claimWrites) != 0 {
		t.Fatalf("invalid claim made %d writes", len(repository.claimWrites))
	}

	validClaim := invalidClaim
	validClaim.RuntimeID = testRuntimeID
	if err := service.ClaimAdapterRuntime(ctx, validClaim); err != nil {
		t.Fatal(err)
	}
	if len(repository.claimWrites) != 1 || repository.claimWrites[0].RuntimeID != testRuntimeID ||
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
	repository.adapterPage = Page[AdapterInstance]{Items: []AdapterInstance{repository.adapter}, HasMore: true}
	repository.healthHistoryPage = Page[HealthTransition]{Items: []HealthTransition{{
		ReceiveOrder: 4, Status: string(AdapterHealthUnhealthy), Source: healthSourceAdapter,
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
	*page.Items[0].Health.SourceObservedAt = now
	page.Items[0].Health.Runtime.SoftwareName = "changed"
	if repository.adapter.Health.Reason.Code != "hearth.network_unreachable" ||
		repository.adapter.Health.SourceObservedAt.Equal(now) ||
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
}

func healthyAdapterFixture(at time.Time) AdapterInstance {
	lastHeartbeatAt := at.Add(-time.Second)
	sourceObservedAt := at.Add(-time.Second)
	return AdapterInstance{ID: "simulator", Health: AdapterHealth{
		Status: AdapterHealthHealthy, Source: healthSourceAdapter,
		Since: at.Add(-time.Minute), EvidenceAt: at, SourceObservedAt: &sourceObservedAt,
		Runtime: &RuntimeEvidence{
			ID: testRuntimeID, Status: runtimeStatusOnline, SoftwareName: "hearth-simulator",
			SoftwareVersion: "0.1.0", ClaimedAt: at.Add(-time.Hour),
			LastHeartbeatAt: &lastHeartbeatAt, LeaseExpiresAt: at.Add(adapterLeaseDuration),
		},
	}}
}
