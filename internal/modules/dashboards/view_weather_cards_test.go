package dashboards_test

import (
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Weather slots that accept a generic numeric sensor are told apart only by
// the Entity's stored unit, so presenting a view checks it.
func TestWeatherSensorSlotsRequireTheirStoredUnit(t *testing.T) {
	t.Parallel()
	const (
		rate      = "ent_01900000-0000-7000-8000-000000000201"
		total     = "ent_01900000-0000-7000-8000-000000000202"
		direction = "ent_01900000-0000-7000-8000-000000000203"
		speed     = "ent_01900000-0000-7000-8000-000000000204"
	)
	sensor := func(id string) string {
		return `{"kind":"entity-state","entity_id":"` + id + `","entity_type":"hearth.numericsensor/v1"}`
	}
	d := cardDefinition(t, `{"type":"Rain","props":{"rate":`+sensor(rate)+`,"totals":{"day":`+sensor(total)+`}}}`)
	wind := cardDefinition(t, `{"type":"Wind","props":{"speed":{"kind":"entity-state","entity_id":"`+speed+
		`","entity_type":"hearth.speed/v1"},"direction":`+sensor(direction)+`}}`)
	units := map[string]string{rate: "mm/h", total: "mm", direction: "deg"}
	for _, tc := range []struct {
		name       string
		definition dashboards.Definition
		swap       map[string]string
		reason     string
	}{
		{name: "matching units", definition: d},
		{name: "matching direction", definition: wind},
		{name: "rate in mm", definition: d, swap: map[string]string{rate: "mm"},
			reason: "weather rain rates need a numeric sensor in mm/h"},
		{name: "total in mm/h", definition: d, swap: map[string]string{total: "mm/h"},
			reason: "weather rain totals need a numeric sensor in mm"},
		{name: "direction in percent", definition: wind, swap: map[string]string{direction: "%RH"},
			reason: "weather wind directions need a numeric sensor in deg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := chartReader{devices.EntityID(speed): {Entity: devices.Entity{
				ID: speed, TypeID: "hearth.speed/v1", Support: devices.EntitySupport(exampleSupport["hearth.speed/v1"]),
			}}}
			for id, unit := range units {
				if swapped, ok := tc.swap[id]; ok {
					unit = swapped
				}
				reader[devices.EntityID(id)] = devices.EntityWithState{Entity: devices.Entity{
					ID:     devices.EntityID(id),
					TypeID: "hearth.numericsensor/v1",
					Support: devices.EntitySupport(
						`{"state":{"minimum":0,"maximum":10000,"unit":"` + unit + `"},"operations":{}}`,
					),
				}}
			}
			_, err := dashboards.NewService(nil, reader, nil).PresentView(t.Context(), tc.definition)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var invalid dashboards.DefinitionError
			if !errors.As(err, &invalid) || invalid.Reason != tc.reason {
				t.Fatalf("present = %v, want %q", err, tc.reason)
			}
		})
	}
}
