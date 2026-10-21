//go:build integration

package sqlite //nolint:testpackage // Tests exercise persisted Adapter runtime behavior.
import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestSQLiteAdapterClaimIsIdempotentAndFencedUntilSupervisorExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewAdapterRepository(database)
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
	if err := repository.ClaimAdapterRuntime(ctx, conflicting); !errors.Is(err, adapters.ErrRuntimeClaimConflict) {
		t.Fatalf("runtime ID reuse with different metadata error = %v", err)
	}

	active := testClaimWrite(testSecondRuntime, claimedAt.Add(time.Second))
	err := repository.ClaimAdapterRuntime(ctx, active)
	var activeErr *adapters.AdapterActiveError
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("active claim error = %v", err)
	}

	takeover := testClaimWrite(testSecondRuntime, first.LeaseExpiresAt)
	err = repository.ClaimAdapterRuntime(ctx, takeover)
	activeErr = nil
	if !errors.As(err, &activeErr) || !activeErr.RetryAfter.Equal(first.LeaseExpiresAt) {
		t.Fatalf("claim before supervisor expiry error = %v", err)
	}
	if expiryErr := repository.ExpireAdapterLeases(ctx, adapters.ExpireLeasesWrite{
		ExpiresAt: first.LeaseExpiresAt,
	}); expiryErr != nil {
		t.Fatal(expiryErr)
	}
	err = repository.ClaimAdapterRuntime(ctx, takeover)
	if err != nil {
		t.Fatalf("takeover claim after supervisor expiry: %v", err)
	}
	if retryErr := repository.ClaimAdapterRuntime(ctx, first); !errors.Is(retryErr, adapters.ErrRuntimeClaimConflict) {
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
	history, err := repository.ListAdapterHealthHistory(ctx, adapters.ListAdapterHealthParams{
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
	repository := NewAdapterRepository(database)
	claimedAt := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	first := testClaimWrite(testRuntimeID, claimedAt)
	if err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}

	overdueAt := first.LeaseExpiresAt.Add(time.Second)
	renewed, err := repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
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
	if adapter.Health.Status != adapters.AdapterHealthHealthy || adapter.Health.Runtime == nil ||
		adapter.Health.Runtime.Status != adapters.RuntimeStatusOnline || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(overdueAt) {
		t.Fatalf("Adapter after overdue heartbeat = %#v", adapter)
	}

	second := testClaimWrite(testSecondRuntime, overdueAt.Add(time.Second))
	claimErr := repository.ClaimAdapterRuntime(ctx, second)
	var activeErr *adapters.AdapterActiveError
	if !errors.As(claimErr, &activeErr) || !activeErr.RetryAfter.Equal(renewed.LeaseExpiresAt) {
		t.Fatalf("claim after overdue heartbeat error = %v", claimErr)
	}
	releasedAt := renewed.LeaseExpiresAt.Add(time.Second)
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, adapters.ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt,
	}); releaseErr != nil {
		t.Fatalf("overdue release: %v", releaseErr)
	}
	adapter, err = repository.GetAdapter(ctx, "simulator")
	if err != nil || adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.stopped" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.Status != adapters.RuntimeStatusOffline {
		t.Fatalf("Adapter after overdue release = %#v, %v", adapter, err)
	}
	assertTableCount(t, database, "health_transitions", 3)
}

func TestSQLiteUnknownHeartbeatReplacesCoreEvidenceWithoutTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewAdapterRepository(database)
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
	if _, err = repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthUnknown,
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
	repository := NewAdapterRepository(database)
	claimedAt := time.Date(2026, 8, 29, 12, 55, 0, 0, time.UTC)
	if err := repository.ClaimAdapterRuntime(ctx, testClaimWrite(testRuntimeID, claimedAt)); err != nil {
		t.Fatal(err)
	}
	healthyAt := claimedAt.Add(time.Second)
	if _, err := repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt, LeaseExpiresAt: healthyAt.Add(testAdapterLeaseDuration),
	}); err != nil {
		t.Fatal(err)
	}
	unknownAt := healthyAt.Add(time.Second)
	_, err := repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthUnknown,
		SourceObservedAt: unknownAt, ReceivedAt: unknownAt, LeaseExpiresAt: unknownAt.Add(testAdapterLeaseDuration),
	})
	if !errors.Is(err, adapters.ErrInvalidHealthTransition) {
		t.Fatalf("return to unknown error = %v", err)
	}
}

func TestSQLiteHeartbeatRefreshesEvidenceWithoutFabricatingTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewAdapterRepository(database)
	claimedAt := time.Date(2026, 8, 29, 13, 0, 0, 0, time.UTC)
	claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testRuntimeID, claimedAt),
	)
	if claimErr != nil {
		t.Fatal(claimErr)
	}

	healthyAt := claimedAt.Add(time.Second)
	result, err := repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt.Add(-time.Second), ReceivedAt: healthyAt,
		LeaseExpiresAt: healthyAt.Add(15 * time.Second),
	})
	if err != nil {
		t.Fatalf("first healthy heartbeat = %#v, %v", result, err)
	}
	if _, repeatErr := repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
		SourceObservedAt: healthyAt, ReceivedAt: healthyAt.Add(time.Second),
		LeaseExpiresAt: healthyAt.Add(16 * time.Second),
	}); repeatErr != nil {
		t.Fatal(repeatErr)
	}
	assertTableCount(t, database, "health_transitions", 2)

	unhealthyAt := healthyAt.Add(2 * time.Second)
	unhealthy := adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthUnhealthy,
		SourceObservedAt: unhealthyAt.Add(-time.Second),
		Reason:           &adapters.HealthReason{Code: "hearth.network_unreachable"},
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
	if adapter.Health.Status != adapters.AdapterHealthUnhealthy || adapter.Health.Source != healthSourceAdapter ||
		adapter.Health.SourceObservedAt == nil ||
		!adapter.Health.SourceObservedAt.Equal(unhealthy.SourceObservedAt) ||
		adapter.Health.Reason == nil || adapter.Health.Reason.Code != "hearth.network_unreachable" ||
		adapter.Health.Runtime == nil || adapter.Health.Runtime.LastHeartbeatAt == nil ||
		!adapter.Health.Runtime.LastHeartbeatAt.Equal(unhealthy.ReceivedAt) {
		t.Fatalf("current Adapter = %#v", adapter)
	}
	history, err := repository.ListAdapterHealthHistory(ctx, adapters.ListAdapterHealthParams{
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
	result, err = repository.RecordAdapterHeartbeat(ctx, adapters.HeartbeatWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ExternalStatus: adapters.AdapterHealthHealthy,
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
	repository := NewAdapterRepository(database)
	claimedAt := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	first := testClaimWrite(testRuntimeID, claimedAt)
	if err := repository.ClaimAdapterRuntime(ctx, first); err != nil {
		t.Fatal(err)
	}
	releasedAt := claimedAt.Add(time.Second)
	release := adapters.ReleaseRuntimeWrite{AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: releasedAt}
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
	if adapter.Health.Status != adapters.AdapterHealthUnhealthy || adapter.Health.Source != healthSourceCore ||
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
		oldReleaseErr, adapters.ErrRuntimeFenced,
	) {
		t.Fatalf("old release after takeover = %v", oldReleaseErr)
	}
	if expiryErr := repository.ExpireAdapterLeases(
		ctx,
		adapters.ExpireLeasesWrite{ExpiresAt: second.LeaseExpiresAt},
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

	history, err := repository.ListAdapterHealthHistory(ctx, adapters.ListAdapterHealthParams{
		AdapterID: "simulator", Limit: 10,
	})
	if err != nil || len(history.Items) != 4 {
		t.Fatalf("Adapter history = %#v, %v", history, err)
	}
}
