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

func (r entityReader) GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error) {
	return r.entity, r.err
}

func TestPresentationChecksBindingsButSaveValidationDoesNot(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Temperature","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"},"display":{"unit":"celsius"}}}}}}`,
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
		{"disabled and unobserved", entityReader{entity: devices.EntityWithState{Entity: devices.Entity{TypeID: "hearth.temperature/v1", Support: devices.EntitySupport(`{"state":{"unit":"mCel"},"operations":{}}`)}}}, true},
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

func TestInvalidRetainedTemperatureSupportIsInternal(t *testing.T) {
	t.Parallel()
	d, err := dashboards.ParseDefinition(
		json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"},"display":{"unit":"celsius"}}}}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dashboards.NewService(nil, entityReader{entity: devices.EntityWithState{Entity: devices.Entity{TypeID: "hearth.temperature/v1", Support: devices.EntitySupport(`{"state":{"unit":"wrong"},"operations":{}}`)}}}).
		PresentView(context.Background(), d)
	if err == nil || errors.Is(err, dashboards.ErrInvalidDefinition) {
		t.Fatalf("corrupt support: %v", err)
	}
}

type countingReader struct{ calls int }

func (r *countingReader) GetEntity(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
	r.calls++
	return devices.EntityWithState{
		Entity: devices.Entity{
			ID:      id,
			TypeID:  "hearth.temperature/v1",
			Support: devices.EntitySupport(`{"state":{"unit":"mCel"},"operations":{}}`),
		},
	}, nil
}

func TestPresentationDeduplicatesEntityReadsAcrossComponents(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"r","elements":{"r":{"type":"Stack","props":{"gap":"small"},"children":["m","c"]},"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"},"display":{"unit":"celsius"}}},"c":{"type":"TimeSeries","props":{"title":"T","series":[{"source":{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1","window":"6h"}}],"display":{"unit":"celsius"}}}}}}`,
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
