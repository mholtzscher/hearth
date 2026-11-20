package dashboards_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const thermostatEntity = "ent_01900000-0000-7000-8000-000000000402"

func thermostatNode(props string) string {
	return `{"type":"Thermostat","props":{"source":{"kind":"entity-state","entity_id":"` + thermostatEntity +
		`","entity_type":"hearth.thermostat/v1"}` + props + `}}`
}

func thermostatReader(entityType, support string) chartReader {
	return chartReader{thermostatEntity: {Entity: devices.Entity{
		ID:      thermostatEntity,
		TypeID:  devices.EntityTypeID(entityType),
		Support: devices.EntitySupport(support),
	}}}
}

func TestThermostatAcceptedAtEveryWritePath(t *testing.T) {
	t.Parallel()
	reader := thermostatReader("hearth.thermostat/v1", exampleSupport["hearth.thermostat/v1"])
	for _, tc := range []struct{ name, node string }{
		{"labeled", thermostatNode(`,"label":"Living room"`)},
		{"minimal", thermostatNode("")},
		{"empty field", `{"type":"Thermostat","props":{"source":{"kind":"entity-state","entity_id":"` +
			thermostatEntity + `","entity_type":"hearth.thermostat/v1","field":""}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, err := range presentCreateSave(t, cardDefinition(t, tc.node), reader) {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestThermostatRejectsAnEntityOfAnotherType(t *testing.T) {
	t.Parallel()
	reader := thermostatReader("hearth.power/v1", `{"state":{},"operations":{"set":{}}}`)
	for _, err := range presentCreateSave(t, cardDefinition(t, thermostatNode("")), reader) {
		var invalid dashboards.DefinitionError
		if !errors.As(err, &invalid) ||
			invalid.Reason != "entity_type must match the Entity, with kind entity-state or entity-series" {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestThermostatDefinitionShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node string }{
		{"another entity_type", `{"type":"Thermostat","props":{"source":{"kind":"entity-state","entity_id":"` +
			thermostatEntity + `","entity_type":"hearth.power/v1"}}}`},
		{"a field", `{"type":"Thermostat","props":{"source":{"kind":"entity-state","entity_id":"` +
			thermostatEntity + `","entity_type":"hearth.thermostat/v1","field":"/mode"}}}`},
		{"unknown prop", thermostatNode(`,"layout":"card"`)},
		{"missing source", `{"type":"Thermostat","props":{"label":"Living room"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := dashboards.ParseDefinition(json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Climate","spec":{"root":"card","elements":{"card":` +
					tc.node + `}}}`,
			))
			var invalid dashboards.DefinitionError
			if !errors.As(err, &invalid) || invalid.Reason != "does not match the view schema" {
				t.Fatalf("error = %v, want schema rejection", err)
			}
		})
	}
}
