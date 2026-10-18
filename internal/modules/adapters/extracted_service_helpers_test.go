package adapters //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"time"
)

// testRuntimeID is the simulator runtime the health and availability fixtures
// claim before they act on an Entity.
const testRuntimeID = RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")

type healthRepositoryStub struct {
	claimWrites       []ClaimRuntimeWrite
	heartbeatWrites   []HeartbeatWrite
	releaseWrites     []ReleaseRuntimeWrite
	expiryWrites      []ExpireLeasesWrite
	adapter           AdapterInstance
	adapterPage       Page[AdapterInstance]
	healthHistoryPage Page[HealthTransition]
	listAdapterCalls  int
	historyCalls      int
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

func healthyAdapterFixture(at time.Time) AdapterInstance {
	lastHeartbeatAt := at.Add(-time.Second)
	sourceObservedAt := at.Add(-time.Second)
	return AdapterInstance{ID: "simulator", Health: AdapterHealth{
		Status: AdapterHealthHealthy, Source: "adapter",
		Since: at.Add(-time.Minute), EvidenceAt: at, SourceObservedAt: &sourceObservedAt,
		Runtime: &RuntimeEvidence{
			ID: testRuntimeID, Status: RuntimeStatusOnline, SoftwareName: "hearth-simulator",
			SoftwareVersion: "0.1.0", ClaimedAt: at.Add(-time.Hour),
			LastHeartbeatAt: &lastHeartbeatAt, LeaseExpiresAt: at.Add(adapterLeaseDuration),
		},
	}}
}
