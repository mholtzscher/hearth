package dashboards_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const coverEntity = "ent_01900000-0000-7000-8000-000000000401"

func coverNode(props string) string {
	return `{"type":"Cover","props":{"source":{"kind":"entity-state","entity_id":"` + coverEntity +
		`","entity_type":"hearth.cover/v1"},` + props + `}}`
}

func coverReader(entityType, support string) chartReader {
	return chartReader{coverEntity: {Entity: devices.Entity{
		ID:      coverEntity,
		TypeID:  devices.EntityTypeID(entityType),
		Support: devices.EntitySupport(support),
	}}}
}

func TestCoverAcceptedAtEveryWritePath(t *testing.T) {
	t.Parallel()
	reader := coverReader("hearth.cover/v1", `{"state":{},"operations":{"set":{},"stop":{}}}`)
	for _, tc := range []struct{ name, props string }{
		{"card", `"layout":"card","label":"Garage door"`},
		{"tile without confirmation", `"layout":"tile","confirm_open":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, err := range presentCreateSave(t, cardDefinition(t, coverNode(tc.props)), reader) {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCoverRejectsAnEntityOfAnotherType(t *testing.T) {
	t.Parallel()
	reader := coverReader("hearth.power/v1", `{"state":{},"operations":{"set":{}}}`)
	for _, err := range presentCreateSave(t, cardDefinition(t, coverNode(`"layout":"card"`)), reader) {
		var invalid dashboards.DefinitionError
		if !errors.As(err, &invalid) ||
			invalid.Reason != "entity_type must match the Entity, with kind entity-state or entity-series" {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestCoverDefinitionShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node string }{
		{"another entity_type", `{"type":"Cover","props":{"layout":"card","source":{"kind":"entity-state","entity_id":"` +
			coverEntity + `","entity_type":"hearth.power/v1"}}}`},
		{"a field", `{"type":"Cover","props":{"layout":"card","source":{"kind":"entity-state","entity_id":"` +
			coverEntity + `","entity_type":"hearth.cover/v1","field":"/position"}}}`},
		{"unknown layout", coverNode(`"layout":"wide"`)},
		{"no layout", coverNode(`"label":"Garage"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := dashboards.ParseDefinition(json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Garage","spec":{"root":"card","elements":{"card":` +
					tc.node + `}}}`,
			))
			if err == nil {
				t.Fatal("definition accepted")
			}
		})
	}
}
