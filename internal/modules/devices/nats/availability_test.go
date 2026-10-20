package nats //nolint:testpackage // Tests exercise package-private NATS wire mappings.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type availabilityRecorder struct {
	requestID string
	adapterID string
	runtimeID adapters.RuntimeID
	reports   []devices.EntityAvailabilityReport
	reported  time.Time
	err       error
}

func (recorder *availabilityRecorder) ReportEntityAvailability(
	_ context.Context,
	requestID string,
	adapterID string,
	runtimeID adapters.RuntimeID,
	reports []devices.EntityAvailabilityReport,
) (time.Time, error) {
	recorder.requestID = requestID
	recorder.adapterID = adapterID
	recorder.runtimeID = runtimeID
	recorder.reports = reports
	return recorder.reported, recorder.err
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
		{name: "fenced", err: adapters.ErrRuntimeFenced, code: "runtime_fenced"},
		{name: "nonhealthy", err: adapters.ErrAdapterUnhealthy, code: "adapter_unhealthy"},
		{name: "invalid request", err: devices.ErrInvalidAvailabilityRequest, code: "invalid_request"},
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
}
