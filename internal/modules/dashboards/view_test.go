package dashboards_test

import (
	"context"
	"encoding/json"
	"errors"
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

// exampleSensorUnits gives the weather example's numeric sensors the stored
// units their slots require; other numeric sensors use exampleSupport.
//
//nolint:gochecknoglobals // Immutable test fixture keyed by Entity ID.
var exampleSensorUnits = map[string]string{
	"ent_01900000-0000-7000-8000-000000000104": "deg",
	"ent_01900000-0000-7000-8000-000000000105": "mm/h",
	"ent_01900000-0000-7000-8000-000000000106": "mm",
	"ent_01900000-0000-7000-8000-000000000107": "mm",
	"ent_01900000-0000-7000-8000-000000000108": "mm",
	"ent_01900000-0000-7000-8000-000000000109": "mm",
	"ent_01900000-0000-7000-8000-000000000110": "mm",
}

// exampleEntities supplies every Entity a definition references, wherever its
// binding sits, with matching type and a supported stored envelope.
// Never-observed and disabled Entities stay valid.
func exampleEntities(definition json.RawMessage) chartReader {
	result := chartReader{}
	var tree any
	if err := json.Unmarshal(definition, &tree); err != nil {
		return result
	}
	var visit func(node any)
	visit = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			id, _ := value["entity_id"].(string)
			entityType, _ := value["entity_type"].(string)
			if id != "" && entityType != "" {
				support := exampleSupport[entityType]
				if unit, ok := exampleSensorUnits[id]; ok {
					support = `{"state":{"minimum":0,"maximum":10000,"unit":"` + unit + `"},"operations":{}}`
				}
				result[devices.EntityID(id)] = devices.EntityWithState{Entity: devices.Entity{
					ID: devices.EntityID(
						id,
					),
					TypeID:  devices.EntityTypeID(entityType),
					Support: devices.EntitySupport(support),
				}}
			}
			for _, child := range value {
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(tree)
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

// An author repairs a rejected view only from its reason, so every rejection
// must name the rule it violated. Schema details can quote submitted values,
// so schema failures share one fixed reason.
func TestRejectedExamplesNameTheViolatedRule(t *testing.T) {
	t.Parallel()
	const (
		schema   = "does not match the view schema"
		graph    = "root and every child must name an existing element, each used once, at most 8 levels deep"
		series   = "a TimeSeries charts distinct Entities of one entity_type and field over one window"
		kind     = "Metric and TimeSeries need a numeric field; State and StateTimeline need a yes/no or text field"
		rowTwice = "a StateTimeline shows each Entity field once"
		weekBool = "WeekPattern needs a yes/no field"
		noField  = "the field is not a State field of the entity_type"
		tooMany  = "a view references at most 16 distinct Entities and Devices"
		noRange  = "range needs a numeric field that declares bounds"
		boolWord = "words need a yes/no field"
		orphan   = "every element must be reachable from root"
	)
	want := map[string]string{
		"forecast-hourly-too-few":          schema,
		"forecast-daily-too-many":          schema,
		"forecast-field":                   schema,
		"forecast-wrong-type":              schema,
		"outside-now-wrong-type":           schema,
		"rain-total-field":                 schema,
		"wind-unknown-slot":                schema,
		"wind-invalid-trend":               schema,
		"toggle-tile-binary-sensor":        schema,
		"toggle-tile-boolean-metric":       "ToggleTile metrics and details need numeric State fields",
		"toggle-list-duplicate-row":        "a ToggleList shows each power Entity once",
		"toggle-list-empty":                schema,
		"range-without-bounds":             noRange,
		"words-on-text":                    boolWord,
		"reject-boolean-field":             kind,
		"reject-string-field":              kind,
		"reject-missing-field":             noField,
		"reject-object-root":               noField,
		"reject-mixed-fields":              series,
		"unknown-component":                schema,
		"unsupported-major":                schema,
		"unknown-property":                 schema,
		"action":                           schema,
		"unreachable":                      orphan,
		"missing-child":                    graph,
		"cycle":                            graph,
		"shared-child":                     graph,
		"depth-nine":                       graph,
		"unknown-binding":                  schema,
		"dynamic-prop":                     schema,
		"oversized-title":                  schema,
		"invalid-entity-id":                schema,
		"duplicate-series":                 series,
		"mixed-series-window":              series,
		"too-many-entity-references":       tooMany,
		"mixed-series-type":                series,
		"unreadable-entity-type":           schema,
		"legacy-display-rejected-on-write": schema,
		"timeline-numeric-row":             kind,
		"timeline-duplicate-row":           rowTwice,
		"timeline-words-on-text":           boolWord,
		"timeline-unknown-style":           schema,
		"week-pattern-text-field":          weekBool,
		"week-pattern-window":              schema,
	}
	var examples []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		if example.Valid {
			continue
		}
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			reason, listed := want[example.Name]
			if !listed {
				t.Fatal("rejected example has no expected reason")
			}
			_, err := dashboards.ParseDefinition(example.Definition)
			var rejected dashboards.DefinitionError
			if !errors.As(err, &rejected) || !errors.Is(err, dashboards.ErrInvalidDefinition) {
				t.Fatalf("error = %v, want a DefinitionError", err)
			}
			if rejected.Reason != reason || err.Error() != "invalid view definition: "+reason {
				t.Fatalf("error = %q, want reason %q", err, reason)
			}
		})
	}
}

func TestPresentationNamesMissingEntity(t *testing.T) {
	t.Parallel()
	d, err := dashboards.ParseDefinition(json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000000","entity_type":"hearth.temperature/v1"}}}}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	missing := entityReader{err: devices.ErrEntityNotFound}
	_, err = dashboards.NewService(nil, missing, nil).PresentView(context.Background(), d)
	var rejected dashboards.DefinitionError
	if !errors.As(err, &rejected) || rejected.Reason != "an entity_id names no Entity" {
		t.Fatalf("error = %v, want the missing-Entity reason", err)
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
