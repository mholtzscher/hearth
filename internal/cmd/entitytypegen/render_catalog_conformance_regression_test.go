package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalogProbePreservesOriginatingSupport exercises an optional operation
// whose supporting case is not first: the disabled case comes first with a
// wider maximum, and the narrower enabled case comes later. The operation
// wiring must reuse the originating (narrow) support, otherwise the valid
// command would resolve against a support without the operation and the
// invalid rep (valid under the wide support) would be wrongly accepted. The
// unequal State probe must also search the later case while keeping the first
// case's valid State as incoming.
func TestCatalogProbePreservesOriginatingSupport(t *testing.T) {
	t.Parallel()
	model := writeDisabledFirstCatalogFixture(t)
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(probe.operations))
	}
	operation := probe.operations[0]
	if string(operation.support) != `{"state":{"maximum":50},"operations":{"set":{"step":5}}}` {
		t.Fatalf("operation support = %s, want narrow enabled support", operation.support)
	}
	if string(operation.parameters) != `{"value":40}` {
		t.Fatalf("parameters = %s, want 40", operation.parameters)
	}
	// 60 decodes against the parameter schema but exceeds the narrow
	// maximum while fitting the wide first-case maximum, so it only
	// rejects when wired to its originating support.
	if string(operation.supportInvalidParams) != `{"value":60}` {
		t.Fatalf("support-invalid parameters = %s, want 60", operation.supportInvalidParams)
	}
	if string(probe.unequalState) != `45` {
		t.Fatalf("unequal State = %s, want later-case outcome State 45", probe.unequalState)
	}
	if string(operation.invalidSupport) != `{"state":{"maximum":50},"operations":{"set":{"step":5}}}` {
		t.Fatalf("invalid parameters support = %s, want narrow enabled support", operation.invalidSupport)
	}
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered.content)
	if !strings.Contains(text, `"maximum\":50`) {
		t.Fatalf("rendered wiring omits the narrow originating support:\n%s", text)
	}
	if !strings.Contains(text, "entitySet := Entity") {
		t.Fatalf("rendered wiring reuses the disabled base entity for the operation:\n%s", text)
	}
	if !strings.Contains(text, "catalog.ResolveCommand(entitySet,") {
		t.Fatalf("rendered wiring does not resolve against the originating entity:\n%s", text)
	}
	if !strings.Contains(text, `catalog.EqualState(entity, Value("45"), Value("40"))`) {
		t.Fatalf("rendered wiring omits the later-case unequal State probe:\n%s", text)
	}
}

// TestCatalogUnequalKeepsValidIncoming protects support narrowing: the only
// unequal candidate is schema-valid but support-invalid (brightness maximum
// 80, recorded outcome 85). EqualState validates incoming only, so the
// generated probe must pass the recorded candidate as persisted and the
// authored valid State as incoming.
func TestCatalogUnequalKeepsValidIncoming(t *testing.T) {
	t.Parallel()
	model := writeNarrowedOutcomeCatalogFixture(t)
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if string(probe.validState) != `75` {
		t.Fatalf("valid State = %s, want 75", probe.validState)
	}
	if string(probe.unequalState) != `85` {
		t.Fatalf("unequal State = %s, want 85", probe.unequalState)
	}
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered.content)
	if !strings.Contains(text, `catalog.EqualState(entity, Value("85"), Value("75"))`) {
		t.Fatalf("rendered unequal probe does not keep valid State incoming:\n%s", text)
	}
}

func writeCatalogSchemas(t *testing.T, directory string) {
	t.Helper()
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:catalog:regression:state", "type": "integer", "minimum": 0, "maximum": 100,
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:catalog:regression:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{
				"type": "object", "additionalProperties": false,
				"required":   []string{"maximum"},
				"properties": map[string]any{"maximum": map[string]any{"type": "integer"}},
			},
			"operations": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"set": map[string]any{"type": "object", "additionalProperties": false},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "set-parameters.schema.json"), map[string]any{
		"$id": "urn:test:catalog:regression:set-parameters", "type": "object", "additionalProperties": false,
		"required": []string{"value"},
		"properties": map[string]any{
			"value": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
	})
}

func writeDisabledFirstCatalogFixture(t *testing.T) entityTypeModel {
	t.Helper()
	directory := t.TempDir()
	writeCatalogSchemas(t, directory)
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	return entityTypeModel{
		Package: "examplev1", Directory: directory, ModuleRoot: directory, TypeID: "example.value/v1",
		StateFile: "state.schema.json",
		Operations: []operationModel{
			{
				Name:           "set",
				GoName:         "Set",
				ParametersFile: "set-parameters.schema.json",
				DeadlineMS:     10000,
				Outcome:        outcomeObserved,
			},
		},
		Examples: examplesFile{Cases: []exampleCase{
			{
				Name:    "disabled",
				Support: raw(`{"state":{"maximum":90},"operations":{}}`),
				States: []validityExample{
					{Value: raw(`40`), Valid: true},
					{Value: raw(`95`), Valid: false},
				},
				Operations: map[string]operationExamples{},
			},
			{
				Name:    "narrow",
				Support: raw(`{"state":{"maximum":50},"operations":{"set":{"step":5}}}`),
				States: []validityExample{
					{Value: raw(`40`), Valid: true},
					{Value: raw(`60`), Valid: false},
				},
				Operations: map[string]operationExamples{"set": {
					Parameters: []validityExample{
						{Value: raw(`{"value":40}`), Valid: true},
						{Value: raw(`{"value":60}`), Valid: false},
					},
					Outcomes: []outcomeExample{
						{Parameters: raw(`{"value":40}`), State: raw(`40`), Satisfied: true},
						{Parameters: raw(`{"value":40}`), State: raw(`45`), Satisfied: false},
					},
				}},
			},
		}},
	}
}

// TestEqualCatalogJSONNumericSpellings proves probe selection compares JSON
// numbers by exact rational value, not spelling, whitespace, or float64
// conversion:
// 1/1.0/1e0 and -0/0 compare equal inside nested arrays and objects, while
// precision beyond 2^53 stays distinct and numbers never collide with
// strings.
func TestEqualCatalogJSONNumericSpellings(t *testing.T) {
	t.Parallel()
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	equalCases := []struct {
		name  string
		left  string
		right string
	}{
		{"integer spellings", `1`, `1.0`},
		{"exponent spelling", `1`, `1e0`},
		{"negative zero", `-0`, `0`},
		{"nested", `{"a":[1.0,{"b":-0}],"c":{"d":7.5e1}}`, `{"c":{"d":75.0},"a":[1,{"b":0}]}`},
		{"fraction spellings", `1.5`, `1.50`},
		{"fixture support", `{"state":{"maximum":8e1}}`, `{"state":{"maximum":80}}`},
	}
	for _, example := range equalCases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			equal, err := equalCatalogJSON(raw(example.left), raw(example.right))
			if err != nil {
				t.Fatal(err)
			}
			if !equal {
				t.Fatalf("equalCatalogJSON(%s, %s) = false, want true", example.left, example.right)
			}
		})
	}
	unequalCases := []struct {
		name  string
		left  string
		right string
	}{
		{"beyond float64", `9007199254740993`, `9007199254740992`},
		{"different integers", `75`, `85`},
		{"number versus string", `75`, `"75"`},
		{"nested number versus string", `{"a":1}`, `{"a":"1"}`},
		{"fraction versus integer", `3`, `1.5`},
	}
	for _, example := range unequalCases {
		t.Run(example.name, func(t *testing.T) {
			t.Parallel()
			equal, err := equalCatalogJSON(raw(example.left), raw(example.right))
			if err != nil {
				t.Fatal(err)
			}
			if equal {
				t.Fatalf("equalCatalogJSON(%s, %s) = true, want false", example.left, example.right)
			}
		})
	}
}

// TestCatalogUnequalSkipsNumericEquivalent proves the unequal selector
// treats 75.0 and 7.5e1 as the same State, falling through to the narrowed
// recorded outcome 85.
func TestCatalogUnequalSkipsNumericEquivalent(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeCatalogSchemas(t, directory)
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	model := entityTypeModel{
		Package: "examplev1", Directory: directory, ModuleRoot: directory, TypeID: "example.value/v1",
		StateFile: "state.schema.json",
		Operations: []operationModel{
			{
				Name:           "set",
				GoName:         "Set",
				ParametersFile: "set-parameters.schema.json",
				DeadlineMS:     10000,
				Outcome:        outcomeObserved,
			},
		},
		Examples: examplesFile{Cases: []exampleCase{{
			Name:    "dim",
			Support: raw(`{"state":{"maximum":8e1},"operations":{"set":{"step":5}}}`),
			States: []validityExample{
				{Value: raw(`75.0`), Valid: true},
				{Value: raw(`7.5e1`), Valid: true},
				{Value: raw(`101`), Valid: false},
			},
			Operations: map[string]operationExamples{"set": {
				Parameters: []validityExample{
					{Value: raw(`{"value":75.0}`), Valid: true},
					{Value: raw(`{"value":76}`), Valid: false},
				},
				Outcomes: []outcomeExample{
					{Parameters: raw(`{"value":75.0}`), State: raw(`75.0`), Satisfied: true},
					{Parameters: raw(`{"value":75.0}`), State: raw(`85`), Satisfied: false},
				},
			}},
		}}},
	}
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if string(probe.validState) != `75.0` {
		t.Fatalf("valid State = %s, want 75.0", probe.validState)
	}
	if string(probe.unequalState) != `85` {
		t.Fatalf("unequal State = %s, want 85", probe.unequalState)
	}
}

func writeNarrowedOutcomeCatalogFixture(t *testing.T) entityTypeModel {
	t.Helper()
	directory := t.TempDir()
	writeCatalogSchemas(t, directory)
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	return entityTypeModel{
		Package: "examplev1", Directory: directory, ModuleRoot: directory, TypeID: "example.value/v1",
		StateFile: "state.schema.json",
		Operations: []operationModel{
			{
				Name:           "set",
				GoName:         "Set",
				ParametersFile: "set-parameters.schema.json",
				DeadlineMS:     10000,
				Outcome:        outcomeObserved,
			},
		},
		Examples: examplesFile{Cases: []exampleCase{{
			Name:    "dim",
			Support: raw(`{"state":{"maximum":80},"operations":{"set":{"step":5}}}`),
			States: []validityExample{
				{Value: raw(`75`), Valid: true},
				{Value: raw(`101`), Valid: false},
			},
			Operations: map[string]operationExamples{"set": {
				Parameters: []validityExample{
					{Value: raw(`{"value":75}`), Valid: true},
					{Value: raw(`{"value":76}`), Valid: false},
				},
				Outcomes: []outcomeExample{
					{Parameters: raw(`{"value":75}`), State: raw(`75`), Satisfied: true},
					{Parameters: raw(`{"value":75}`), State: raw(`85`), Satisfied: false},
				},
			}},
		}}},
	}
}
