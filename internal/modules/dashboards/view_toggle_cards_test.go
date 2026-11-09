package dashboards_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func TestToggleTileRequiresMetricsFromItsDevice(t *testing.T) {
	t.Parallel()
	const power = "ent_01900000-0000-7000-8000-000000000001"
	const metric = "ent_01900000-0000-7000-8000-000000000002"
	const detail = "ent_01900000-0000-7000-8000-000000000003"
	d := cardDefinition(
		t,
		`{"type":"ToggleTile","props":{"source":{"kind":"entity-state","entity_id":"`+power+`","entity_type":"hearth.power/v1"},"metric":{"source":{"kind":"entity-state","entity_id":"`+metric+`","entity_type":"hearth.numericsensor/v1"},"window":"24h","detail":{"label":"Total energy","source":{"kind":"entity-state","entity_id":"`+detail+`","entity_type":"hearth.numericsensor/v1"}}}}}`,
	)
	for _, tc := range []struct{ name, other string }{
		{"same Device", ""}, {"metric elsewhere", metric}, {"detail elsewhere", detail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := toggleCardDeviceReader(t, d, tc.other)
			_, err := dashboards.NewService(nil, reader, nil).PresentView(t.Context(), d)
			if tc.other == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var invalid dashboards.DefinitionError
			if !errors.As(err, &invalid) ||
				invalid.Reason != "ToggleTile metrics and details must belong to the power Entity's Device" {
				t.Fatalf("present = %v", err)
			}
		})
	}
}

func toggleCardDeviceReader(t *testing.T, definition dashboards.Definition, other string) chartReader {
	t.Helper()
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	reader := exampleEntities(raw)
	for id, entity := range reader {
		entity.Entity.DeviceID = "dev_01900000-0000-7000-8000-000000000001"
		if string(id) == other {
			entity.Entity.DeviceID = "dev_01900000-0000-7000-8000-000000000002"
		}
		reader[id] = entity
	}
	return reader
}

func TestToggleCardsRespectTheCombinedReferenceLimit(t *testing.T) {
	t.Parallel()
	for _, count := range []int{16, 17} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			t.Parallel()
			rows := make([]map[string]any, 0, count-2)
			for index := 3; index <= count; index++ {
				rows = append(
					rows,
					map[string]any{
						"source": map[string]any{
							"kind":        "entity-state",
							"entity_id":   fmt.Sprintf("ent_01900000-0000-7000-8000-%012d", index),
							"entity_type": "hearth.power/v1",
						},
					},
				)
			}
			// A metering tile contributes two references; lists share the same budget.
			definition := map[string]any{
				"schema_version":  1,
				"catalog_version": "hearth.dashboard/v1",
				"title":           "Budget",
				"spec": map[string]any{"root": "root", "elements": map[string]any{
					"root": map[string]any{
						"type":     "Stack",
						"props":    map[string]any{"gap": "small"},
						"children": []string{"tile", "first", "last"},
					},
					"tile": map[string]any{
						"type": "ToggleTile",
						"props": map[string]any{
							"source": map[string]any{
								"kind":        "entity-state",
								"entity_id":   "ent_01900000-0000-7000-8000-000000000001",
								"entity_type": "hearth.power/v1",
							},
							"metric": map[string]any{
								"source": map[string]any{
									"kind":        "entity-state",
									"entity_id":   "ent_01900000-0000-7000-8000-000000000002",
									"entity_type": "hearth.numericsensor/v1",
								},
								"window": "24h",
							},
						},
					},
					"first": map[string]any{
						"type":  "ToggleList",
						"props": map[string]any{"title": "First", "rows": rows[:12]},
					},
					"last": map[string]any{
						"type":  "ToggleList",
						"props": map[string]any{"title": "Last", "rows": rows[12:]},
					},
				}},
			}
			raw, err := json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			_, err = dashboards.ParseDefinition(raw)
			if count == 16 && err != nil {
				t.Fatal(err)
			}
			if count == 17 && !errors.Is(err, dashboards.ErrInvalidDefinition) {
				t.Fatalf("parse = %v", err)
			}
		})
	}
}
