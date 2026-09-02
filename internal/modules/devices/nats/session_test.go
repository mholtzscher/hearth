package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type lifecycleRecorder struct {
	mutex        sync.Mutex
	claim        devices.ClaimAdapterRuntimeParams
	heartbeat    devices.AdapterHeartbeat
	releasedID   devices.RuntimeID
	claimErr     error
	heartbeatErr error
	releaseErr   error
}

func (recorder *lifecycleRecorder) ClaimAdapterRuntime(
	_ context.Context,
	params devices.ClaimAdapterRuntimeParams,
) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.claim = params
	return recorder.claimErr
}

func (recorder *lifecycleRecorder) RecordAdapterHeartbeat(
	_ context.Context,
	heartbeat devices.AdapterHeartbeat,
) (devices.HeartbeatResult, error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.heartbeat = heartbeat
	return devices.HeartbeatResult{
		LeaseExpiresAt: time.Date(2026, 8, 29, 15, 0, 15, 0, time.UTC),
	}, recorder.heartbeatErr
}

func (recorder *lifecycleRecorder) ReleaseAdapterRuntime(
	_ context.Context,
	_ string,
	runtimeID devices.RuntimeID,
) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.releasedID = runtimeID
	return recorder.releaseErr
}

func TestSessionServerMapsClaimHeartbeatAndRelease(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	recorder := &lifecycleRecorder{}
	server, err := StartSessionServer(connection, validator, recorder, recorder, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	claimID := "clm_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	claim := requestLifecycle[adapterClaimRequest, adapterClaimResponse](
		t, connection, validator,
		contractsv1.AdapterClaimRequestSchemaID, contractsv1.AdapterClaimResponseSchemaID,
		claimID, natswire.AdapterClaimSubject,
		adapterClaimRequest{
			AdapterID: "simulator", RuntimeID: testRuntimeID,
			SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
		},
	)
	if claim.Data.Status != statusAccepted {
		t.Fatalf("claim response = %#v", claim.Data)
	}

	heartbeatSubject := func(adapterID string) (string, error) {
		return natswire.AdapterHeartbeatSubject(adapterID, testRuntimeID)
	}
	heartbeat := requestLifecycle[adapterHeartbeatRequest, adapterHeartbeatResponse](
		t, connection, validator,
		contractsv1.AdapterHeartbeatRequestSchemaID, contractsv1.AdapterHeartbeatResponseSchemaID,
		"hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab", heartbeatSubject,
		adapterHeartbeatRequest{ExternalSystem: externalSystemHealth{
			Status: "unhealthy", SourceObservedAt: "2026-08-29T15:00:00Z",
			Reason: &healthReason{Code: "hearth.network_unreachable"},
		}},
	)
	if heartbeat.Data.Status != statusAccepted || heartbeat.Data.LeaseExpiresAt != "2026-08-29T15:00:15Z" {
		t.Fatalf("heartbeat response = %#v", heartbeat.Data)
	}

	releaseSubject := func(adapterID string) (string, error) {
		return natswire.AdapterReleaseSubject(adapterID, testRuntimeID)
	}
	release := requestLifecycle[adapterReleaseRequest, adapterReleaseResponse](
		t, connection, validator,
		contractsv1.AdapterReleaseRequestSchemaID, contractsv1.AdapterReleaseResponseSchemaID,
		"rel_01890f47-7a6b-7c4d-8e9f-0123456789ab", releaseSubject, adapterReleaseRequest{},
	)
	if release.Data.Status != statusAccepted {
		t.Fatalf("release response = %#v", release.Data)
	}

	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.claim.AdapterID != "simulator" || recorder.claim.RuntimeID != devices.RuntimeID(testRuntimeID) ||
		recorder.claim.SoftwareName != "hearth-simulator" || recorder.claim.SoftwareVersion != "0.1.0" {
		t.Fatalf("mapped claim = %#v", recorder.claim)
	}
	if recorder.heartbeat.RuntimeID != devices.RuntimeID(testRuntimeID) ||
		recorder.heartbeat.ExternalStatus != devices.AdapterHealthUnhealthy ||
		recorder.heartbeat.Reason == nil ||
		recorder.heartbeat.Reason.Code != "hearth.network_unreachable" {
		t.Fatalf("mapped heartbeat = %#v", recorder.heartbeat)
	}
	if recorder.releasedID != devices.RuntimeID(testRuntimeID) {
		t.Fatalf("released runtime = %q", recorder.releasedID)
	}
}

func TestSessionServerMapsLifecycleRejections(t *testing.T) {
	t.Parallel()
	retryAfter := time.Date(2026, 8, 29, 15, 0, 15, 0, time.UTC)
	claim, handled := mapClaimResult(&devices.AdapterActiveError{RetryAfter: retryAfter})
	if !handled || claim.Error == nil || claim.Error.Code != "adapter_active" ||
		claim.Error.RetryAfter == nil || *claim.Error.RetryAfter != retryAfter.Format(time.RFC3339Nano) {
		t.Fatalf("active claim mapping = %#v, %t", claim, handled)
	}
	conflict, handled := mapClaimResult(devices.ErrRuntimeClaimConflict)
	if !handled || conflict.Error == nil || conflict.Error.Code != "claim_conflict" ||
		conflict.Error.RetryAfter != nil {
		t.Fatalf("claim conflict mapping = %#v, %t", conflict, handled)
	}
	heartbeat, handled := mapHeartbeatResult(devices.HeartbeatResult{}, devices.ErrRuntimeFenced)
	if !handled || heartbeat.Error == nil || heartbeat.Error.Code != "runtime_fenced" {
		t.Fatalf("fenced heartbeat mapping = %#v, %t", heartbeat, handled)
	}
	invalidHeartbeat, handled := mapHeartbeatResult(
		devices.HeartbeatResult{}, devices.ErrInvalidHealthTransition,
	)
	if !handled || invalidHeartbeat.Error == nil || invalidHeartbeat.Error.Code != "invalid_transition" {
		t.Fatalf("invalid heartbeat mapping = %#v, %t", invalidHeartbeat, handled)
	}
	release, handled := mapReleaseResult(devices.ErrRuntimeFenced)
	if !handled || release.Error == nil || release.Error.Code != "runtime_fenced" {
		t.Fatalf("fenced release mapping = %#v, %t", release, handled)
	}
	if _, infrastructureHandled := mapReleaseResult(errors.New("SQLite unavailable")); infrastructureHandled {
		t.Fatal("infrastructure failure unexpectedly produced a schema reply")
	}
}

func startAdapterSessionServer(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
) *lifecycleRecorder {
	t.Helper()
	recorder := &lifecycleRecorder{}
	server, err := StartSessionServer(connection, validator, recorder, recorder, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })
	return recorder
}

func (recorder *lifecycleRecorder) claimedRuntimeID() devices.RuntimeID {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.claim.RuntimeID
}

func requestLifecycle[Req, Resp any](
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	requestSchema string,
	responseSchema string,
	requestID string,
	subjectFor func(string) (string, error),
	data Req,
) natswire.Envelope[Resp] {
	t.Helper()
	subject, err := subjectFor("simulator")
	if err != nil {
		t.Fatal(err)
	}
	request := natswire.Envelope[Req]{
		ID: requestID, Schema: requestSchema, EmittedAt: "2026-08-29T15:00:00Z",
		CorrelationID: testCorrelationID, Data: data,
	}
	payload, err := natswire.Encode(validator, requestSchema, request)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(t.Context(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[Resp](validator, responseSchema, reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	if response.CausationID == nil || *response.CausationID != requestID ||
		response.CorrelationID != testCorrelationID {
		t.Fatalf("response causality = %#v", response)
	}
	return response
}
