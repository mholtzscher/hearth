//go:build integration

package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"context"
	"log/slog"
	"testing"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func TestSessionServerMapsClaimHeartbeatAndRelease(t *testing.T) {
	t.Parallel()
	broker := natstest.StartServer(t)
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	recorder := &lifecycleRecorder{}
	server, err := StartSessionServer(connection, validator, recorder, recorder, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })

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
	if recorder.claim.AdapterID != "simulator" || recorder.claim.RuntimeID != adapters.RuntimeID(testRuntimeID) ||
		recorder.claim.SoftwareName != "hearth-simulator" || recorder.claim.SoftwareVersion != "0.1.0" {
		t.Fatalf("mapped claim = %#v", recorder.claim)
	}
	if recorder.heartbeat.RuntimeID != adapters.RuntimeID(testRuntimeID) ||
		recorder.heartbeat.ExternalStatus != adapters.AdapterHealthUnhealthy ||
		recorder.heartbeat.Reason == nil ||
		recorder.heartbeat.Reason.Code != "hearth.network_unreachable" {
		t.Fatalf("mapped heartbeat = %#v", recorder.heartbeat)
	}
	if recorder.releasedID != adapters.RuntimeID(testRuntimeID) {
		t.Fatalf("released runtime = %q", recorder.releasedID)
	}
	if len(recorder.publicationMetadata) != 3 {
		t.Fatalf("publication metadata count = %d", len(recorder.publicationMetadata))
	}
	for index, requestID := range []string{claimID, "hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab", "rel_01890f47-7a6b-7c4d-8e9f-0123456789ab"} {
		metadata := recorder.publicationMetadata[index]
		if metadata.CorrelationID != testCorrelationID || metadata.CausationID == nil ||
			*metadata.CausationID != requestID {
			t.Fatalf("publication metadata for %s = %#v", requestID, metadata)
		}
	}
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
