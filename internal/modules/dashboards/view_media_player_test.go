package dashboards_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const mediaPlayerEntity = "ent_01900000-0000-7000-8000-000000000403"

func mediaPlayerNode(props string) string {
	return `{"type":"MediaPlayer","props":{"source":{"kind":"entity-state","entity_id":"` + mediaPlayerEntity +
		`","entity_type":"hearth.mediaplayer/v1"}` + props + `}}`
}

func mediaPlayerReader(entityType, support string) chartReader {
	return chartReader{mediaPlayerEntity: {Entity: devices.Entity{
		ID:      mediaPlayerEntity,
		TypeID:  devices.EntityTypeID(entityType),
		Support: devices.EntitySupport(support),
	}}}
}

func TestMediaPlayerAcceptedAtEveryWritePath(t *testing.T) {
	t.Parallel()
	reader := mediaPlayerReader("hearth.mediaplayer/v1", exampleSupport["hearth.mediaplayer/v1"])
	for _, tc := range []struct{ name, node string }{
		{"card", mediaPlayerNode(`,"layout":"card","label":"Living room"`)},
		{"tile", mediaPlayerNode(`,"layout":"tile"`)},
		{"minimal", mediaPlayerNode("")},
		{"empty field", `{"type":"MediaPlayer","props":{"source":{"kind":"entity-state","entity_id":"` +
			mediaPlayerEntity + `","entity_type":"hearth.mediaplayer/v1","field":""}}}`},
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

func TestMediaPlayerRejectsAnEntityOfAnotherType(t *testing.T) {
	t.Parallel()
	reader := mediaPlayerReader("hearth.power/v1", `{"state":{},"operations":{"set":{}}}`)
	for _, err := range presentCreateSave(t, cardDefinition(t, mediaPlayerNode("")), reader) {
		var invalid dashboards.DefinitionError
		if !errors.As(err, &invalid) ||
			invalid.Reason != "entity_type must match the Entity, with kind entity-state or entity-series" {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestMediaPlayerDefinitionShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node string }{
		{"another entity_type", `{"type":"MediaPlayer","props":{"source":{"kind":"entity-state","entity_id":"` +
			mediaPlayerEntity + `","entity_type":"hearth.power/v1"}}}`},
		{"a field", `{"type":"MediaPlayer","props":{"source":{"kind":"entity-state","entity_id":"` +
			mediaPlayerEntity + `","entity_type":"hearth.mediaplayer/v1","field":"/playback"}}}`},
		{"unknown layout", mediaPlayerNode(`,"layout":"grid"`)},
		{"unknown prop", mediaPlayerNode(`,"confirm_open":false`)},
		{"missing source", `{"type":"MediaPlayer","props":{"label":"Living room"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := dashboards.ParseDefinition(json.RawMessage(
				`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Music","spec":{"root":"card","elements":{"card":` +
					tc.node + `}}}`,
			))
			var invalid dashboards.DefinitionError
			if !errors.As(err, &invalid) || invalid.Reason != "does not match the view schema" {
				t.Fatalf("error = %v, want schema rejection", err)
			}
		})
	}
}
