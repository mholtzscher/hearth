package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const (
	testRuntimeID     = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

type lifecycleRecorder struct {
	mutex               sync.Mutex
	claim               adapters.ClaimAdapterRuntimeParams
	heartbeat           adapters.AdapterHeartbeat
	releasedID          adapters.RuntimeID
	claimErr            error
	heartbeatErr        error
	releaseErr          error
	publicationMetadata []adapters.PendingHealthUpdate
}

func (recorder *lifecycleRecorder) ClaimAdapterRuntime(
	ctx context.Context,
	params adapters.ClaimAdapterRuntimeParams,
) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	metadata, err := adapters.HealthPublicationMetadata(ctx)
	if err != nil {
		return err
	}
	recorder.publicationMetadata = append(recorder.publicationMetadata, metadata)
	recorder.claim = params
	return recorder.claimErr
}

func (recorder *lifecycleRecorder) RecordAdapterHeartbeat(
	ctx context.Context,
	heartbeat adapters.AdapterHeartbeat,
) (adapters.HeartbeatResult, error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	metadata, err := adapters.HealthPublicationMetadata(ctx)
	if err != nil {
		return adapters.HeartbeatResult{}, err
	}
	recorder.publicationMetadata = append(recorder.publicationMetadata, metadata)
	recorder.heartbeat = heartbeat
	return adapters.HeartbeatResult{
		LeaseExpiresAt: time.Date(2026, 8, 29, 15, 0, 15, 0, time.UTC),
	}, recorder.heartbeatErr
}

func (recorder *lifecycleRecorder) ReleaseAdapterRuntime(
	ctx context.Context,
	_ string,
	runtimeID adapters.RuntimeID,
) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	metadata, err := adapters.HealthPublicationMetadata(ctx)
	if err != nil {
		return err
	}
	recorder.publicationMetadata = append(recorder.publicationMetadata, metadata)
	recorder.releasedID = runtimeID
	return recorder.releaseErr
}

func TestSessionServerMapsLifecycleRejections(t *testing.T) {
	t.Parallel()
	retryAfter := time.Date(2026, 8, 29, 15, 0, 15, 0, time.UTC)
	claim, handled := mapClaimResult(&adapters.AdapterActiveError{RetryAfter: retryAfter})
	if !handled || claim.Error == nil || claim.Error.Code != "adapter_active" ||
		claim.Error.RetryAfter == nil || *claim.Error.RetryAfter != retryAfter.Format(time.RFC3339Nano) {
		t.Fatalf("active claim mapping = %#v, %t", claim, handled)
	}
	conflict, handled := mapClaimResult(adapters.ErrRuntimeClaimConflict)
	if !handled || conflict.Error == nil || conflict.Error.Code != "claim_conflict" ||
		conflict.Error.RetryAfter != nil {
		t.Fatalf("claim conflict mapping = %#v, %t", conflict, handled)
	}
	heartbeat, handled := mapHeartbeatResult(adapters.HeartbeatResult{}, adapters.ErrRuntimeFenced)
	if !handled || heartbeat.Error == nil || heartbeat.Error.Code != "runtime_fenced" {
		t.Fatalf("fenced heartbeat mapping = %#v, %t", heartbeat, handled)
	}
	invalidHeartbeat, handled := mapHeartbeatResult(
		adapters.HeartbeatResult{}, adapters.ErrInvalidHealthTransition,
	)
	if !handled || invalidHeartbeat.Error == nil || invalidHeartbeat.Error.Code != "invalid_transition" {
		t.Fatalf("invalid heartbeat mapping = %#v, %t", invalidHeartbeat, handled)
	}
	release, handled := mapReleaseResult(adapters.ErrRuntimeFenced)
	if !handled || release.Error == nil || release.Error.Code != "runtime_fenced" {
		t.Fatalf("fenced release mapping = %#v, %t", release, handled)
	}
	if _, infrastructureHandled := mapReleaseResult(errors.New("SQLite unavailable")); infrastructureHandled {
		t.Fatal("infrastructure failure unexpectedly produced a schema reply")
	}
}
