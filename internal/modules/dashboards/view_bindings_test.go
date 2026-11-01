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
		{"disabled and unobserved", entityReader{entity: devices.EntityWithState{Entity: devices.Entity{TypeID: "hearth.temperature/v1"}}}, true},
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
