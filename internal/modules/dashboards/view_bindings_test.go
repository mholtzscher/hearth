package dashboards_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type entityReader struct {
	entity devices.EntityWithState
	err    error
}

func (r entityReader) GetEntity(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
	typed := r.entity
	if typed.Entity.TypeID != "" {
		typed.Entity.ID = id
	}
	return typed, r.err
}

func TestPresentationChecksBindingsButSaveValidationDoesNot(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Temperature","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"}}}}}}`,
	)
	d, err := dashboards.ParseDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		reader entityReader
		valid  bool
	}{
		{"missing", entityReader{err: devices.ErrEntityNotFound}, false},
		{"wrong type", entityReader{entity: devices.EntityWithState{Entity: devices.Entity{TypeID: "hearth.other/v1"}}}, false},
		{"disabled and unobserved", entityReader{entity: devices.EntityWithState{Entity: devices.Entity{TypeID: "hearth.temperature/v1", Support: devices.EntitySupport(`{"state":{},"operations":{}}`)}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			artifact, presentErr := dashboards.NewService(nil, tc.reader).PresentView(context.Background(), d)
			if (presentErr == nil) != tc.valid {
				t.Fatalf("present: %v", presentErr)
			}
			if tc.valid {
				b, marshalErr := json.Marshal(artifact)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if _, parseErr := dashboards.ParseArtifact(b); parseErr != nil {
					t.Fatal(parseErr)
				}
			} else if !errors.Is(presentErr, dashboards.ErrInvalidDefinition) {
				t.Fatalf("classification: %v", presentErr)
			}
		})
	}
}

type countingReader struct{ calls int }

func (r *countingReader) GetEntity(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
	r.calls++
	return devices.EntityWithState{
		Entity: devices.Entity{
			ID:      id,
			TypeID:  "hearth.temperature/v1",
			Support: devices.EntitySupport(`{"state":{},"operations":{}}`),
		},
	}, nil
}

func TestPresentationDeduplicatesEntityReadsAcrossComponents(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"r","elements":{"r":{"type":"Stack","props":{"gap":"small"},"children":["m","c"]},"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"}}},"c":{"type":"TimeSeries","props":{"title":"T","series":[{"source":{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1","window":"6h"}}]}}}}}`,
	)
	d, err := dashboards.ParseDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	reader := &countingReader{}
	if _, presentErr := dashboards.NewService(nil, reader).PresentView(context.Background(), d); presentErr != nil {
		t.Fatal(presentErr)
	}
	if reader.calls != 1 {
		t.Fatalf("reads = %d", reader.calls)
	}
}

// humiditySupport is one admitted stored Support shape for the humidity Entity.
//
//nolint:gochecknoglobals // Immutable test fixture shared by tests in this file.
var humiditySupport = devices.EntitySupport(`{"state":{},"operations":{}}`)

func TestPresentationRejectsMixedChartsAndLegacyDisplays(t *testing.T) {
	t.Parallel()
	entityID := "ent_01900000-0000-7000-8000-000000000000"
	otherID := "ent_01900000-0000-7000-8000-000000000001"
	for _, tc := range []struct {
		name, props string
	}{
		{"mixed chart types", `{"title":"Chart","series":[{"source":{"kind":"entity-series","entity_id":"` + entityID + `","entity_type":"hearth.temperature/v1","window":"1h"}},{"source":{"kind":"entity-series","entity_id":"` + otherID + `","entity_type":"hearth.relativehumidity/v1","window":"1h"}}]}`},
		{"legacy display", `{"title":"Chart","series":[{"source":{"kind":"entity-series","entity_id":"` + entityID + `","entity_type":"hearth.relativehumidity/v1","window":"1h"}}],"display":{"unit":"percent"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"c","elements":{"c":{"type":"TimeSeries","props":` + tc.props + `}}}}`,
			)
			if _, parseErr := dashboards.ParseDefinition(raw); parseErr == nil {
				t.Fatalf("accepted binding mix")
			}
		})
	}
	d, err := dashboards.ParseDefinition(json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"h","elements":{"h":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"` + entityID + `","entity_type":"hearth.relativehumidity/v1"}}}}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	reader := entityReader{
		entity: devices.EntityWithState{
			Entity: devices.Entity{
				ID:      devices.EntityID(entityID),
				TypeID:  "hearth.relativehumidity/v1",
				Support: humiditySupport,
			},
		},
	}
	if _, presentErr := dashboards.NewService(nil, reader).PresentView(context.Background(), d); presentErr != nil {
		t.Fatal(presentErr)
	}
}

type chartReader map[devices.EntityID]devices.EntityWithState

func (r chartReader) GetEntity(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
	return r[id], nil
}

func TestChartCanonicalUnitCompatibility(t *testing.T) {
	t.Parallel()
	const first = "ent_01900000-0000-7000-8000-000000000000"
	const second = "ent_01900000-0000-7000-8000-000000000001"
	for _, tc := range []struct {
		name, entityType, firstSupport, secondSupport string
		valid                                         bool
	}{
		{"same dynamic unit", "hearth.numericsensor/v1", `{"state":{"unit":"lqi"}}`, `{"state":{"unit":"lqi"}}`, true},
		{"different dynamic unit", "hearth.numericsensor/v1", `{"state":{"unit":"Cel"}}`, `{"state":{"unit":"[degF]"}}`, false},
		{"different scales same percentage axis", "hearth.brightness/v1", `{"state":{"maximum":99}}`, `{"state":{"maximum":100}}`, true},
		{"missing scale", "hearth.brightness/v1", `{"state":{"maximum":99}}`, `{"state":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Chart","spec":{"root":"c","elements":{"c":{"type":"TimeSeries","props":{"title":"Chart","series":[{"source":{"kind":"entity-series","entity_id":"` + first + `","entity_type":"` + tc.entityType + `","window":"6h"}},{"source":{"kind":"entity-series","entity_id":"` + second + `","entity_type":"` + tc.entityType + `","field":"","window":"6h"}}]}}}}}`,
			)
			d, err := dashboards.ParseDefinition(raw)
			if err != nil {
				t.Fatal(err)
			}
			reader := chartReader{
				first: {
					Entity: devices.Entity{
						ID:      first,
						TypeID:  devices.EntityTypeID(tc.entityType),
						Support: devices.EntitySupport(tc.firstSupport),
					},
				},
				second: {
					Entity: devices.Entity{
						ID:      second,
						TypeID:  devices.EntityTypeID(tc.entityType),
						Support: devices.EntitySupport(tc.secondSupport),
					},
				},
			}
			_, err = dashboards.NewService(nil, reader).PresentView(context.Background(), d)
			if (err == nil) != tc.valid {
				t.Fatalf("present = %v, valid = %v", err, tc.valid)
			}
			if err != nil && !errors.Is(err, dashboards.ErrInvalidDefinition) {
				t.Fatalf("classification: %v", err)
			}
		})
	}
}
