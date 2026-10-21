//go:build integration

package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"log/slog"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestEntityAvailabilityServerMapsAcceptedBatch(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	reportedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	recorder := &availabilityRecorder{reported: reportedAt}
	server, err := StartEntityAvailabilityServer(
		connection, validator, recorder, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })
	subjectFor := func(adapterID string) (string, error) {
		return natswire.EntityAvailabilitySubject(adapterID, testRuntimeID)
	}
	response := requestDeviceRequest[entityAvailabilityRequest, entityAvailabilityResponse](
		t, connection, validator,
		contractsv1.EntityAvailabilityRequestSchemaID, contractsv1.EntityAvailabilityResponseSchemaID,
		"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab", subjectFor,
		entityAvailabilityRequest{Entities: []entityAvailabilityEntry{{
			EntityID: testEntityID, Status: "unavailable", SourceObservedAt: "2026-08-29T15:00:00Z",
			Reason: &healthReason{Code: "hearth.entity_unavailable"},
		}}},
	)
	if response.Data.Status != statusAccepted || response.Data.ReportedAt != reportedAt.Format(time.RFC3339Nano) ||
		response.Data.Count != 1 {
		t.Fatalf("availability response = %#v", response.Data)
	}
	if recorder.requestID != "avl_01890f47-7a6b-7c4d-8e9f-0123456789ab" ||
		recorder.adapterID != "simulator" || recorder.runtimeID != adapters.RuntimeID(testRuntimeID) ||
		len(recorder.reports) != 1 || recorder.reports[0].EntityID != devices.EntityID(testEntityID) ||
		recorder.reports[0].Reason == nil ||
		recorder.reports[0].Reason.Code != "hearth.entity_unavailable" {
		t.Fatalf("mapped availability report = %#v", recorder)
	}
}
