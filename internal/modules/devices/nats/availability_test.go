package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type availabilityRecorder struct {
	adapterID string
	runtimeID devices.RuntimeID
	reports   []devices.EntityAvailabilityReport
	reported  time.Time
	err       error
}

func (recorder *availabilityRecorder) ReportEntityAvailability(
	_ context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	reports []devices.EntityAvailabilityReport,
) (time.Time, error) {
	recorder.adapterID = adapterID
	recorder.runtimeID = runtimeID
	recorder.reports = reports
	return recorder.reported, recorder.err
}

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
	detail := "Home Assistant reported unavailable"
	subjectFor := func(adapterID string) (string, error) {
		return natswire.EntityAvailabilitySubject(adapterID, testRuntimeID)
	}
	response := requestLifecycle[entityAvailabilityRequest, entityAvailabilityResponse](
		t, connection, validator,
		contractsv1.EntityAvailabilityRequestSchemaID, contractsv1.EntityAvailabilityResponseSchemaID,
		"avl_01890f47-7a6b-7c4d-8e9f-0123456789ab", subjectFor,
		entityAvailabilityRequest{Entities: []entityAvailabilityEntry{{
			EntityID: testEntityID, Status: "unavailable", SourceObservedAt: "2026-08-29T15:00:00Z",
			Reason: &healthReason{Code: "hearth.entity_unavailable", Detail: &detail},
		}}},
	)
	if response.Data.Status != statusAccepted || response.Data.ReportedAt != reportedAt.Format(time.RFC3339Nano) ||
		response.Data.Count != 1 {
		t.Fatalf("availability response = %#v", response.Data)
	}
	if recorder.adapterID != "simulator" || recorder.runtimeID != devices.RuntimeID(testRuntimeID) ||
		len(recorder.reports) != 1 || recorder.reports[0].EntityID != devices.EntityID(testEntityID) ||
		recorder.reports[0].Reason == nil || recorder.reports[0].Reason.Detail == nil ||
		*recorder.reports[0].Reason.Detail != detail {
		t.Fatalf("mapped availability report = %#v", recorder)
	}
}

func TestEntityAvailabilityServerMapsOnlyContractRejections(t *testing.T) {
	t.Parallel()
	entityID := devices.EntityID(testEntityID)
	for _, test := range []struct {
		name     string
		err      error
		code     string
		entityID string
	}{
		{name: "fenced", err: devices.ErrRuntimeFenced, code: "runtime_fenced"},
		{name: "nonhealthy", err: devices.ErrAdapterUnhealthy, code: "adapter_unhealthy"},
		{name: "unknown Entity", err: &devices.EntityAvailabilityReportError{
			EntityID: entityID, Err: devices.ErrEntityNotFound,
		}, code: "unknown_entity", entityID: testEntityID},
		{name: "wrong Adapter", err: &devices.EntityAvailabilityReportError{
			EntityID: entityID, Err: devices.ErrEntityWrongAdapter,
		}, code: "wrong_adapter", entityID: testEntityID},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response, handled := mapAvailabilityResult(time.Time{}, 1, test.err)
			if !handled || response.Status != statusRejected || response.Error == nil ||
				response.Error.Code != test.code || response.Error.EntityID != test.entityID {
				t.Fatalf("mapped rejection = %#v, handled %t", response, handled)
			}
		})
	}
	if _, handled := mapAvailabilityResult(time.Time{}, 1, errors.New("SQLite unavailable")); handled {
		t.Fatal("infrastructure failure unexpectedly produced a schema reply")
	}
	if _, handled := mapAvailabilityResult(time.Time{}, 1, devices.ErrHealthEvaluationPaused); handled {
		t.Fatal("paused evaluation unexpectedly produced a schema reply")
	}
}
