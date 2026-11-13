package dashboards_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func cardDefinition(t *testing.T, node string) dashboards.Definition {
	t.Helper()
	d, err := dashboards.ParseDefinition(
		json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Cards","spec":{"root":"card","elements":{"card":` + node + `}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNewCardsValidateFieldsAtWriteBoundary(t *testing.T) {
	t.Parallel()
	const id = `"entity_id":"ent_01900000-0000-7000-8000-000000000001"`
	for _, tc := range []struct {
		name, node string
		valid      bool
	}{
		{"boolean", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.binarysensor/v1"},"words":{"true":"Open","false":"Closed"}}}`, true},
		{"choice", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.enumsetting/v1"}}}`, true},
		{"text sensor", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.textsensor/v1"}}}`, true},
		{"rgb active", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.colorrgb/v1","field":"/active"}}}`, true},
		{"rgb red", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.colorrgb/v1","field":"/red"}}}`, true},
		{"rgb green", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.colorrgb/v1","field":"/green"}}}`, true},
		{"rgb blue", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.colorrgb/v1","field":"/blue"}}}`, true},
		{"rgb object root", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.colorrgb/v1"}}}`, false},
		{"numeric State", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.temperature/v1"}}}`, false},
		{"words on string", `{"type":"State","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.enumsetting/v1"},"words":{"true":"A","false":"B"}}}`, false},
		{"range percent", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.brightness/v1"},"range":true,"trend":"24h"}}`, true},
		{"no range", `{"type":"Metric","props":{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.temperature/v1"},"range":true}}`, false},
		{"mixed list", `{"type":"ReadingList","props":{"title":"House","rows":[{"source":{"kind":"entity-state",` + id + `,"entity_type":"hearth.temperature/v1"}},{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000002","entity_type":"hearth.power/v1"}}]}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Cards","spec":{"root":"card","elements":{"card":` + tc.node + `}}}`,
			)
			d, err := dashboards.ParseDefinition(raw)
			if (err == nil) != tc.valid {
				t.Fatalf("parse = %v", err)
			}
			if tc.valid {
				_, err = dashboards.NewService(nil, exampleEntities(raw), nil).PresentView(t.Context(), d)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type deviceReader struct{ err error }

func (r deviceReader) GetDevice(_ context.Context, _ devices.GetDeviceParams) (devices.DeviceAggregate, error) {
	return devices.DeviceAggregate{}, r.err
}

func TestDeviceCardChecksExistenceAndPreservesDependencyErrors(t *testing.T) {
	t.Parallel()
	d := cardDefinition(
		t,
		`{"type":"Device","props":{"device":{"kind":"device","device_id":"dev_01900000-0000-7000-8000-000000000001"}}}`,
	)
	failed := errors.New("device read failed")
	for _, tc := range []struct {
		name      string
		err, want error
	}{
		{"present", nil, nil}, {"missing", devices.ErrDeviceNotFound, dashboards.ErrInvalidDefinition}, {"failed", failed, failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := dashboards.NewService(nil, nil, deviceReader{err: tc.err}).PresentView(t.Context(), d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("present = %v, want %v", err, tc.want)
			}
		})
	}
}
