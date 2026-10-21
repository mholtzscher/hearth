package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	testRuntimeID           = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testEntityID            = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testObservationID       = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testSecondObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	testThirdObservationID  = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ad"
	testFourthObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ae"
	testCorrelationID       = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

type projectorFunc func(
	context.Context,
	string,
	adapters.RuntimeID,
	devices.Observation,
	time.Time,
) (devices.ProjectionResult, error)

func (projector projectorFunc) ProjectObservation(
	ctx context.Context,
	adapterID string,
	runtimeID adapters.RuntimeID,
	observation devices.Observation,
	observedAt time.Time,
) (devices.ProjectionResult, error) {
	return projector(ctx, adapterID, runtimeID, observation, observedAt)
}

type projectedObservation struct {
	adapterID   string
	runtimeID   adapters.RuntimeID
	observation devices.Observation
	observedAt  time.Time
}

func TestDomainObservationCopiesWireDataAndPointers(t *testing.T) {
	t.Parallel()
	commandID := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	wire := natswire.Envelope[observation]{
		ID: testObservationID, CorrelationID: testCorrelationID,
		Data: observation{
			EntityID: testEntityID, Value: json.RawMessage(`true`), RefreshForCommand: &commandID,
		},
	}
	sourceUpdatedAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	trace := devices.DeviceFactTraceContext{Traceparent: testFactTraceparent, Tracestate: testFactTracestate}
	mapped, err := domainObservation(wire, sourceUpdatedAt.Add(time.Second), &sourceUpdatedAt, trace)
	if err != nil {
		t.Fatal(err)
	}
	wire.Data.Value[0] = 'x'
	commandID = "changed"
	sourceUpdatedAt = time.Time{}
	if string(mapped.Value) != "true" || mapped.RefreshForCommand == nil ||
		*mapped.RefreshForCommand != devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab") ||
		mapped.CorrelationID != devices.CorrelationID(testCorrelationID) ||
		mapped.SourceUpdatedAt == nil || mapped.SourceUpdatedAt.IsZero() ||
		mapped.Trace != trace {
		t.Fatalf("mapped observation = %#v", mapped)
	}

	// A wire correlation is a canonical identity: mapping must reject a value
	// that is not one, because the accepted Observation fact carries it.
	malformed := wire
	malformed.CorrelationID = "not-a-correlation"
	malformed.Data.RefreshForCommand = nil
	if mapped2, malformedErr := domainObservation(
		malformed, sourceUpdatedAt.Add(time.Second), nil, devices.DeviceFactTraceContext{},
	); malformedErr == nil {
		t.Fatalf("malformed wire correlation was mapped as %q", mapped2.CorrelationID)
	}
}

func mustObservationSubject(t *testing.T) string {
	t.Helper()
	subject, err := natswire.ObservationSubject("simulator", testRuntimeID, testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

const testLinkedCommandID = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func indexProjectedByObservationID(
	t *testing.T,
	projected []map[string]any,
) map[string]map[string]any {
	t.Helper()
	byID := make(map[string]map[string]any)
	for _, record := range projected {
		if record["level"] != "DEBUG" {
			t.Fatalf("projected level = %#v, want DEBUG (record = %#v)", record["level"], record)
		}
		id, _ := record["observation_id"].(string)
		byID[id] = record
		if record["adapter_id"] != "simulator" || record["entity_id"] != testEntityID {
			t.Fatalf("projected record = %#v", record)
		}
	}
	return byID
}

func requireProjectedDisposition(
	t *testing.T,
	byID map[string]map[string]any,
	observationID, disposition string,
) {
	t.Helper()
	record := byID[observationID]
	if record == nil {
		t.Fatalf("missing projected record for %s: %#v", observationID, byID)
	}
	if record["disposition"] != disposition {
		t.Fatalf("disposition for %s = %#v, want %q", observationID, record["disposition"], disposition)
	}
}

func requireLinkedProjection(t *testing.T, linked map[string]any) {
	t.Helper()
	if linked["command_id"] != testLinkedCommandID || linked["correlation_id"] != testCorrelationID {
		t.Fatalf("linked projected record = %#v", linked)
	}
}
