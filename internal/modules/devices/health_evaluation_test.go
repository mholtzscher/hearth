package devices //nolint:testpackage // Tests exercise package-private health evaluation state.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type healthRepositoryStub struct {
	*stubRegistrationRepository

	claimWrites       []ClaimRuntimeWrite
	heartbeatWrites   []HeartbeatWrite
	releaseWrites     []ReleaseRuntimeWrite
	expiryWrites      []ExpireLeasesWrite
	archiveWrites     []ArchiveAdapterParams
	adapter           AdapterInstance
	adapterPage       Page[AdapterInstance]
	healthHistoryPage Page[HealthTransition]
	listAdapterCalls  int
	historyCalls      int
}

func newHealthRepositoryStub() *healthRepositoryStub {
	return &healthRepositoryStub{stubRegistrationRepository: &stubRegistrationRepository{}}
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

func TestServiceHealthEvaluationFreezesWritesAndOverridesRecoveryReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	service := NewService(repository, nil, firstLightCatalog(t), Dependencies{Now: func() time.Time { return now }})
	heartbeat := AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: now,
	}

	if _, err := service.RecordAdapterHeartbeat(ctx, heartbeat); !errors.Is(err, ErrHealthEvaluationPaused) {
		t.Fatalf("heartbeat while paused error = %v", err)
	}
	if len(repository.heartbeatWrites) != 0 {
		t.Fatal("paused heartbeat reached repository")
	}

	resumedAt := now.Add(time.Second)
	service.ResumeHealthEvaluation(resumedAt)
	now = resumedAt.Add(time.Second)
	adapter, err := service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthUnknown ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.core_recovering" ||
		!adapter.Health.Since.Equal(resumedAt) || repository.adapter.Health.Status != AdapterHealthHealthy {
		t.Fatalf("recovery Adapter = %#v, repository Adapter = %#v", adapter, repository.adapter)
	}

	result, err := service.RecordAdapterHeartbeat(ctx, heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RefreshEntityAvailability {
		t.Fatal("first recovery heartbeat did not request Entity availability refresh")
	}
	adapter, err = service.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health == nil || adapter.Health.Status != AdapterHealthHealthy {
		t.Fatalf("Adapter after recovery heartbeat = %#v, %v", adapter, err)
	}

	service.PauseHealthEvaluation()
	releaseErr := service.ReleaseAdapterRuntime(ctx, "simulator", testRuntimeID)
	if !errors.Is(releaseErr, ErrHealthEvaluationPaused) {
		t.Fatalf("release while paused error = %v", releaseErr)
	}
	if len(repository.releaseWrites) != 0 {
		t.Fatal("paused release reached repository")
	}
}

func TestServiceRecoveryGraceDefersLeaseExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resumedAt := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	service := NewService(repository, nil, firstLightCatalog(t), Dependencies{})
	service.ResumeHealthEvaluation(resumedAt)

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
	result, err := service.RecordAdapterHeartbeat(ctx, AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: atBoundary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RefreshEntityAvailability {
		t.Fatal("heartbeat after completed recovery requested a recovery refresh")
	}
}

func TestServiceValidatesClaimsAndAdapterReasonNamespace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	generated := 0
	service := NewService(repository, nil, firstLightCatalog(t), Dependencies{
		Now: func() time.Time { return now },
		NewRuntimeID: func() (RuntimeID, error) {
			generated++
			return testRuntimeID, nil
		},
	})
	service.ResumeHealthEvaluation(now)

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

	wrongNamespace := AdapterHeartbeat{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: now,
		Reason:           &HealthReason{Code: "adapter.some-other-software.connection_lost"},
	}
	if _, err := service.RecordAdapterHeartbeat(ctx, wrongNamespace); err == nil {
		t.Fatal("wrong Adapter reason namespace was accepted")
	}
	if len(repository.heartbeatWrites) != 0 {
		t.Fatal("invalid heartbeat reached repository")
	}

	detail := "upstream disconnected"
	validHeartbeat := wrongNamespace
	validHeartbeat.Reason = &HealthReason{
		Code: "adapter.hearth-simulator.connection_lost", Detail: &detail,
	}
	if _, err := service.RecordAdapterHeartbeat(ctx, validHeartbeat); err != nil {
		t.Fatal(err)
	}
	detail = "mutated"
	write := repository.heartbeatWrites[0]
	if write.Reason == nil || write.Reason.Detail == nil || *write.Reason.Detail != "upstream disconnected" ||
		!write.LeaseExpiresAt.Equal(now.Add(adapterLeaseDuration)) {
		t.Fatalf("heartbeat write = %#v", write)
	}
}

func TestServiceAdapterReadsReturnOwnedCopiesAndValidatePages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	detail := "network unreachable"
	sourceObservedAt := now.Add(-time.Second)
	repository := newHealthRepositoryStub()
	repository.adapter = healthyAdapterFixture(now)
	repository.adapter.Health.Status = AdapterHealthUnhealthy
	repository.adapter.Health.Reason = &HealthReason{Code: "hearth.network_unreachable", Detail: &detail}
	repository.adapter.Health.ExternalSystem.Status = AdapterHealthUnhealthy
	repository.adapter.Health.ExternalSystem.Reason = &HealthReason{
		Code: "hearth.network_unreachable", Detail: &detail,
	}
	repository.adapterPage = Page[AdapterInstance]{Items: []AdapterInstance{repository.adapter}, HasMore: true}
	repository.healthHistoryPage = Page[HealthTransition]{Items: []HealthTransition{{
		ReceiveOrder: 4, Status: string(AdapterHealthUnhealthy), Source: "external_system",
		Reason:           &HealthReason{Code: "hearth.network_unreachable", Detail: &detail},
		SourceObservedAt: &sourceObservedAt, ObservedAt: now,
	}}, HasMore: true}
	service := NewService(repository, nil, firstLightCatalog(t), Dependencies{Now: func() time.Time { return now }})

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
	*page.Items[0].Health.Reason.Detail = "changed"
	page.Items[0].Health.Runtime.SoftwareName = "changed"
	if *repository.adapter.Health.Reason.Detail != detail ||
		repository.adapter.Health.Runtime.SoftwareName != "hearth-simulator" {
		t.Fatal("Adapter list result aliases repository data")
	}

	history, err := service.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || !history.HasMore || len(history.Items) != 1 {
		t.Fatalf("health history = %#v, %v", history, err)
	}
	*history.Items[0].Reason.Detail = "changed"
	*history.Items[0].SourceObservedAt = now
	if *repository.healthHistoryPage.Items[0].Reason.Detail != detail ||
		!repository.healthHistoryPage.Items[0].SourceObservedAt.Equal(sourceObservedAt) {
		t.Fatal("health history result aliases repository data")
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
