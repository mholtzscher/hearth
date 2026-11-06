package dashboards_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// exampleSupport holds one admitted stored Support shape per numeric Entity
// type referenced by the shared examples.
//
//nolint:gochecknoglobals // Immutable test fixture keyed by Entity type.
var exampleSupport = map[string]string{
	"hearth.temperature/v1":      `{"state":{},"operations":{}}`,
	"hearth.relativehumidity/v1": `{"state":{},"operations":{}}`,
	"hearth.pressure/v1":         `{"state":{},"operations":{}}`,
	"hearth.speed/v1":            `{"state":{},"operations":{}}`,
	"hearth.numericsensor/v1":    `{"state":{"minimum":0,"maximum":100,"unit":"%RH"},"operations":{}}`,
	"hearth.brightness/v1":       `{"state":{},"operations":{"set":{}}}`,
	"hearth.colorhs/v1":          `{"state":{},"operations":{"set":{}}}`,
	"hearth.colorxy/v1":          `{"state":{},"operations":{"set":{}}}`,
	"hearth.colortemp/v1":        `{"state":{"minimum":153,"maximum":500},"operations":{"set":{"step":1}}}`,
	"hearth.numericsetting/v1":   `{"state":{"minimum":0,"maximum":100,"choices":[]},"operations":{"set":{}}}`,
	"hearth.binarysensor/v1":     `{"state":{},"operations":{}}`,
	"hearth.power/v1":            `{"state":{},"operations":{"set":{}}}`,
	"hearth.colormode/v1":        `{"state":{},"operations":{"set":{}}}`,
	"hearth.enumsetting/v1":      `{"state":{"choices":["Auto"]},"operations":{"set":{}}}`,
}

// exampleEntities supplies each referenced Entity with matching type and a
// supported stored envelope. Never-observed and disabled Entities stay valid.
func exampleEntities(definition json.RawMessage) chartReader {
	var probe struct {
		Spec struct {
			Elements map[string]struct {
				Type  string `json:"type"`
				Props struct {
					Source struct {
						EntityID   string `json:"entity_id"`
						EntityType string `json:"entity_type"`
					} `json:"source"`
					Series []struct {
						Source struct {
							EntityID   string `json:"entity_id"`
							EntityType string `json:"entity_type"`
						} `json:"source"`
					} `json:"series"`
					Rows []struct {
						Source struct {
							EntityID   string `json:"entity_id"`
							EntityType string `json:"entity_type"`
						} `json:"source"`
					} `json:"rows"`
				} `json:"props"`
			} `json:"elements"`
		} `json:"spec"`
	}
	result := chartReader{}
	if err := json.Unmarshal(definition, &probe); err != nil {
		return result
	}
	for _, node := range probe.Spec.Elements {
		for _, row := range node.Props.Rows {
			result[devices.EntityID(row.Source.EntityID)] = devices.EntityWithState{Entity: devices.Entity{
				ID: devices.EntityID(
					row.Source.EntityID,
				),
				TypeID:  devices.EntityTypeID(row.Source.EntityType),
				Support: devices.EntitySupport(exampleSupport[row.Source.EntityType]),
			}}
		}
		if node.Props.Source.EntityType != "" {
			result[devices.EntityID(node.Props.Source.EntityID)] = devices.EntityWithState{Entity: devices.Entity{
				ID:      devices.EntityID(node.Props.Source.EntityID),
				TypeID:  devices.EntityTypeID(node.Props.Source.EntityType),
				Support: devices.EntitySupport(exampleSupport[node.Props.Source.EntityType]),
			}}
		}
		for _, series := range node.Props.Series {
			if series.Source.EntityType != "" {
				result[devices.EntityID(series.Source.EntityID)] = devices.EntityWithState{Entity: devices.Entity{
					ID:      devices.EntityID(series.Source.EntityID),
					TypeID:  devices.EntityTypeID(series.Source.EntityType),
					Support: devices.EntitySupport(exampleSupport[series.Source.EntityType]),
				}}
			}
		}
	}
	return result
}

func (r chartReader) GetDevice(_ context.Context, params devices.GetDeviceParams) (devices.DeviceAggregate, error) {
	return devices.DeviceAggregate{Device: devices.Device{ID: params.ID}}, nil
}

func TestSharedExamples(t *testing.T) {
	t.Parallel()
	var examples []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			d, err := dashboards.ParseDefinition(example.Definition)
			if (err == nil) != example.Valid {
				t.Fatalf("valid=%v err=%v", example.Valid, err)
			}
			if example.Valid {
				reader := exampleEntities(example.Definition)
				if _, presentErr := dashboards.NewService(nil, reader, reader).
					PresentView(context.Background(), d); presentErr != nil {
					t.Fatal(presentErr)
				}
			}
			if _, readErr := dashboards.ReadDefinition(example.Definition); readErr != nil {
				t.Fatalf("recoverable: %v", readErr)
			}
		})
	}
}

func TestReadDefinitionChecksEnvelopeButPreservesInvalidGraph(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"schema_version":1,"catalog_version":"future","title":123,"spec":{}}`,
		`{"schema_version":"1","catalog_version":"future","title":"x","spec":{}}`,
		`{"schema_version":1,"catalog_version":{},"title":"x","spec":{}}`,
	} {
		if _, err := dashboards.ReadDefinition(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw := json.RawMessage(`{"schema_version":2,"catalog_version":"future","title":"Future","spec":[],"future":true}`)
	got, err := dashboards.ReadDefinition(raw)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("read: %s %v", got, err)
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr == nil {
		t.Fatal("invalid graph accepted for writes")
	}
	oversize := json.RawMessage(
		`{"schema_version":1,"catalog_version":"x","title":"` + strings.Repeat(
			"x",
			dashboards.MaxDefinitionBytes,
		) + `","spec":{}}`,
	)
	if _, readErr := dashboards.ReadDefinition(oversize); readErr == nil {
		t.Fatal("oversize retained definition accepted")
	}
	if _, parseErr := dashboards.ParseDefinition(oversize); parseErr == nil {
		t.Fatal("oversize write accepted")
	}
}

func TestDefinitionSizeLimitWithOtherwiseValidUnicodeNodes(t *testing.T) {
	t.Parallel()
	children := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	elements := map[string]any{
		"root": map[string]any{"type": "Stack", "props": map[string]any{"gap": "small"}, "children": children},
	}
	for _, key := range children {
		elements[key] = map[string]any{"type": "Note", "props": map[string]any{"text": strings.Repeat("🌡", 480)}}
	}
	value := map[string]any{
		"schema_version":  1,
		"catalog_version": "hearth.dashboard/v1",
		"title":           "Size",
		"spec":            map[string]any{"root": "root", "elements": elements},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= dashboards.MaxDefinitionBytes {
		t.Fatalf("fixture is not oversized: %d", len(raw))
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr == nil {
		t.Fatal("oversized valid graph accepted")
	}
	children = children[:8]
	elements["root"].(map[string]any)["children"] = children
	delete(elements, "i")
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr != nil {
		t.Fatalf("under-limit control: %v", parseErr)
	}
}

func TestSharedReferenceLimitFixtureHasValidSixteenEntityControl(t *testing.T) {
	t.Parallel()
	var examples []struct {
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		if example.Name != "too-many-entity-references" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(example.Definition, &value); err != nil {
			t.Fatal(err)
		}
		elements := value["spec"].(map[string]any)["elements"].(map[string]any)
		delete(elements, "c")
		elements["r"].(map[string]any)["children"] = []string{"a", "b"}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, parseErr := dashboards.ParseDefinition(raw); parseErr != nil {
			t.Fatalf("16-reference control: %v", parseErr)
		}
		return
	}
	t.Fatal("missing shared reference fixture")
}
