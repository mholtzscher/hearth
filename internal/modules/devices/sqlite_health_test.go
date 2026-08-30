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

func TestSQLiteAdapterClaimIsIdempotentAndFencedByLease(t *testing.T) {
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
	claim, err := repository.ClaimAdapterRuntime(ctx, takeover)
	if err != nil || claim.RuntimeID != testSecondRuntime {
		t.Fatalf("takeover claim = %#v, %v", claim, err)
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
	if err != nil || !result.RefreshEntityAvailability {
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

	firstDetail := "dial failed"
	unhealthyAt := healthyAt.Add(2 * time.Second)
	unhealthy := HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt.Add(-time.Second),
		Reason:           &HealthReason{Code: "hearth.network_unreachable", Detail: &firstDetail},
		ReceivedAt:       unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}
	if _, unhealthyErr := repository.RecordAdapterHeartbeat(ctx, unhealthy); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	latestDetail := "network remains unreachable"
	unhealthy.Reason.Detail = &latestDetail
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
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthUnhealthy ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Detail == nil ||
		*adapter.Health.Reason.Detail != latestDetail || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(unhealthy.ReceivedAt) {
		t.Fatalf("current Adapter = %#v", adapter)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Items[0].Reason == nil || history.Items[0].Reason.Detail == nil ||
		*history.Items[0].Reason.Detail != firstDetail {
		t.Fatalf("historical transition was rewritten = %#v", history.Items[0])
	}

	recoveredAt := unhealthy.ReceivedAt.Add(time.Second)
	result, err = repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	})
	if err != nil || !result.RefreshEntityAvailability {
		t.Fatalf("recovery heartbeat = %#v, %v", result, err)
	}
	var epoch int
	if scanErr := database.QueryRowContext(ctx,
		"SELECT availability_epoch FROM adapter_instances WHERE adapter_id = 'simulator'",
	).Scan(&epoch); scanErr != nil {
		t.Fatal(scanErr)
	}
	if epoch != 2 {
		t.Fatalf("availability epoch = %d, want 2", epoch)
	}
}

//nolint:gocognit,gocyclo,cyclop // The lifecycle assertions are easier to audit in chronological order.
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
	if adapter.Health == nil || adapter.Health.Status != AdapterHealthUnhealthy ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.stopped" ||
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
	if adapter.Health == nil || adapter.Health.Reason == nil ||
		adapter.Health.Reason.Code != "hearth.heartbeat_expired" {
		t.Fatalf("expired Adapter = %#v", adapter)
	}

	archivedAt := second.LeaseExpiresAt.Add(time.Second)
	if archiveErr := repository.ArchiveAdapter(ctx, ArchiveAdapterParams{
		AdapterID: "simulator", ArchivedAt: archivedAt,
	}); archiveErr != nil {
		t.Fatal(archiveErr)
	}
	if repeatedArchiveErr := repository.ArchiveAdapter(ctx, ArchiveAdapterParams{
		AdapterID: "simulator", ArchivedAt: archivedAt.Add(time.Second),
	}); repeatedArchiveErr != nil {
		t.Fatalf("idempotent archive: %v", repeatedArchiveErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.ArchivedAt == nil || adapter.Health != nil {
		t.Fatalf("archived Adapter = %#v, %v", adapter, err)
	}
	current, err := repository.ListAdapters(ctx, ListAdaptersParams{Limit: 10})
	if err != nil || len(current.Items) != 0 {
		t.Fatalf("default Adapter list = %#v, %v", current, err)
	}
	includingArchived, err := repository.ListAdapters(ctx, ListAdaptersParams{
		Limit: 10, IncludeArchived: true,
	})
	if err != nil || len(includingArchived.Items) != 1 || includingArchived.Items[0].Health != nil {
		t.Fatalf("archived Adapter list = %#v, %v", includingArchived, err)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || len(history.Items) != 4 {
		t.Fatalf("archived Adapter history = %#v, %v", history, err)
	}
	_, err = repository.ClaimAdapterRuntime(ctx, testClaimWrite(
		testThirdClaimID,
		testThirdRuntime,
		archivedAt.Add(time.Second),
	))
	if !errors.Is(err, ErrAdapterArchived) {
		t.Fatalf("archived claim error = %v", err)
	}
}

func TestSQLiteAvailabilityBatchRollsBackAndUsesHealthEpoch(t *testing.T) {
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
	service := NewService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, heartbeatErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	}); heartbeatErr != nil {
		t.Fatal(heartbeatErr)
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
	assertTableCount(t, database, "health_transitions", 3)

	unhealthyAt := reportedAt.Add(time.Second)
	if _, unhealthyErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt, Reason: &HealthReason{Code: "hearth.external_system_unavailable"},
		ReceivedAt: unhealthyAt, LeaseExpiresAt: unhealthyAt.Add(15 * time.Second),
	}); unhealthyErr != nil {
		t.Fatal(unhealthyErr)
	}
	recoveredAt := unhealthyAt.Add(time.Second)
	if _, recoveredErr := repository.RecordAdapterHeartbeat(ctx, HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: AdapterHealthHealthy,
		SourceObservedAt: recoveredAt, ReceivedAt: recoveredAt,
		LeaseExpiresAt: recoveredAt.Add(15 * time.Second),
	}); recoveredErr != nil {
		t.Fatal(recoveredErr)
	}
	if _, recoveryReportErr := repository.ReportEntityAvailability(ctx, AvailabilityBatchWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID,
		Reports: []EntityAvailabilityReport{available}, ReportedAt: recoveredAt.Add(time.Second),
	}); recoveryReportErr != nil {
		t.Fatal(recoveryReportErr)
	}
	var epoch, transitionCount int
	if scanErr := database.QueryRowContext(ctx, `
		SELECT availability_epoch,
		       (SELECT count(*) FROM health_transitions WHERE resource_kind = 'entity')
		FROM entity_availability_current
		WHERE entity_id = ?`, binding.Entities[0].EntityID,
	).Scan(&epoch, &transitionCount); scanErr != nil {
		t.Fatal(scanErr)
	}
	if epoch != 2 || transitionCount != 2 {
		t.Fatalf("availability after recovery = epoch %d, transitions %d", epoch, transitionCount)
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
