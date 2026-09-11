package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEventSourceFixture materializes a minimal event-source Entity type in a
// temporary module: stateless, no Operations, and one required events.names
// list. Tests mutate the copies to exercise authoring failures.
func writeEventSourceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.test\n\ngo 1.26\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writeManifestSchema(t, root)
	directory := filepath.Join(root, "entitytypes", "fixtureeventv1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:event:state", "type": "object",
		"maxProperties": 0, "additionalProperties": false,
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), eventSourceSupportSchema())
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{map[string]any{
			"name": "buttons",
			"support": map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single_press", "double_press"}},
			},
			"states": []any{
				map[string]any{"value": map[string]any{}, "valid": true},
				map[string]any{"value": map[string]any{"unexpected": true}, "valid": false},
			},
			"operations": map[string]any{},
		}},
	})
	manifest := map[string]any{
		"manifest_version": 1,
		"type":             "fixture.event/v1",
		"state_schema":     "state.schema.json",
		"support_schema":   "support.schema.json",
		"stateless":        true,
		"event_source":     true,
		"operations":       map[string]any{},
		"examples":         "examples.json",
	}
	writeJSON(t, filepath.Join(directory, "entitytype.json"), manifest)
	return directory, filepath.Join(directory, "entitytype.json")
}

func eventSourceSupportSchema() map[string]any {
	return map[string]any{
		"$id": "urn:test:event:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations", "events"},
		"properties": map[string]any{
			"state": map[string]any{
				"type": "object", "maxProperties": 0, "additionalProperties": false,
			},
			"operations": map[string]any{
				"type": "object", "maxProperties": 0, "additionalProperties": false,
			},
			"events": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"names"},
				"properties": map[string]any{
					"names": map[string]any{
						"type": "array", "minItems": 1, "maxItems": 64, "uniqueItems": true,
						"items": map[string]any{
							"type": "string", "pattern": entityEventNamePattern,
							"minLength": 1, "maxLength": entityEventNameLength,
						},
					},
				},
			},
		},
	}
}

func TestEventSourceManifestLoadsFixedSupportShape(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeEventSourceFixture(t)
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !model.EventSource || !model.Stateless {
		t.Fatalf("event source = %v, stateless = %v", model.EventSource, model.Stateless)
	}
	if len(model.Operations) != 0 {
		t.Fatalf("operations = %d, want 0", len(model.Operations))
	}
	if names, exists := model.EventSupportSchema.Properties["names"]; !exists || names.Type != schemaTypeArray {
		t.Fatalf("event support schema = %#v", model.EventSupportSchema)
	}
}

// TestEventSourceManifestRejectsAuthoringMistakes pins the fixed event-source
// authoring contract: stateless, no Operations, one required events.names slug
// list, and no event support on closed types.
func TestEventSourceManifestRejectsAuthoringMistakes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		mutate  func(t *testing.T, directory, manifestPath string)
		wantErr string
	}{
		{
			name: "stateful event source",
			mutate: func(t *testing.T, _ string, manifestPath string) {
				patchManifestTopLevel(t, manifestPath, "stateless", false)
			},
			wantErr: "event_source requires stateless: true",
		},
		{
			name: "state is not required",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				schema["required"] = []string{"operations", "events"}
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support schema must require state and operations",
		},
		{
			name: "state schema is not empty",
			mutate: func(t *testing.T, directory, _ string) {
				writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
					"$id": "urn:test:event:state", "type": "object",
					"additionalProperties": false,
					"properties":           map[string]any{"value": map[string]any{"type": "boolean"}},
				})
			},
			wantErr: "event_source state schema must declare no properties",
		},
		{
			name: "state schema is open",
			mutate: func(t *testing.T, directory, _ string) {
				writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
					"$id": "urn:test:event:state", "type": "object", "additionalProperties": true,
				})
			},
			wantErr: "event_source state schema",
		},
		{
			name: "state support is not empty",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				state := schema["properties"].(map[string]any)["state"].(map[string]any)
				delete(state, "maxProperties")
				state["additionalProperties"] = false
				state["properties"] = map[string]any{"value": map[string]any{"type": "boolean"}}
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "event_source support.state must declare no properties",
		},
		{
			name: "state support is open",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				state := schema["properties"].(map[string]any)["state"].(map[string]any)
				delete(state, "maxProperties")
				state["additionalProperties"] = true
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "event_source support.state",
		},
		{
			name: "operations are refused",
			mutate: func(t *testing.T, directory, manifestPath string) {
				patchManifestTopLevel(t, manifestPath, "operations", map[string]any{
					"set": map[string]any{
						"parameters_schema": "set-parameters.schema.json",
						"deadline_ms":       10000,
						"outcome":           "dispatched",
						"satisfied_when":    []any{},
					},
				})
				writeJSON(t, filepath.Join(directory, "set-parameters.schema.json"), map[string]any{
					"$id": "urn:test:event:set", "type": "object", "additionalProperties": false,
				})
			},
			wantErr: "event_source requires an empty operations map",
		},
		{
			name: "missing events property",
			mutate: func(t *testing.T, directory, _ string) {
				writeJSON(t, filepath.Join(directory, "support.schema.json"), minimalSupportSchema("urn:test:event:support"))
			},
			wantErr: "event_source requires a support.events property",
		},
		{
			name: "optional events",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				schema["required"] = []string{"state", "operations"}
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: `event_source requires support to require "events"`,
		},
		{
			name: "open events object",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				events := schema["properties"].(map[string]any)["events"].(map[string]any)
				events["additionalProperties"] = true
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support.events schema",
		},
		{
			name: "extra events property",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				events := schema["properties"].(map[string]any)["events"].(map[string]any)
				events["properties"].(map[string]any)["values"] = map[string]any{"type": "array"}
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: `support.events has unsupported property "values"`,
		},
		{
			name: "non-unique names",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				names := eventSourceNamesSchema(schema)
				names["uniqueItems"] = false
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support.events.names must set uniqueItems",
		},
		{
			name: "unbounded name count",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				names := eventSourceNamesSchema(schema)
				names["maxItems"] = 128
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support.events.names must accept 1 to 64 names",
		},
		{
			name: "loose name pattern",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				items := eventSourceNamesSchema(schema)["items"].(map[string]any)
				items["pattern"] = "^.*$"
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support.events.names items must use pattern",
		},
		{
			name: "names are not strings",
			mutate: func(t *testing.T, directory, _ string) {
				schema := eventSourceSupportSchema()
				names := eventSourceNamesSchema(schema)
				names["items"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 10}
				writeJSON(t, filepath.Join(directory, "support.schema.json"), schema)
			},
			wantErr: "support.events.names must describe a string array",
		},
		{
			name: "examples without names",
			mutate: func(t *testing.T, directory, _ string) {
				writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
					"cases": []any{map[string]any{
						"name": "buttons",
						"support": map[string]any{
							"state": map[string]any{}, "operations": map[string]any{},
							"events": map[string]any{"names": []any{}},
						},
						"states": []any{
							map[string]any{"value": map[string]any{}, "valid": true},
							map[string]any{"value": map[string]any{"unexpected": true}, "valid": false},
						},
						"operations": map[string]any{},
					}},
				})
			},
			wantErr: "support has no events.names",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory, manifestPath := writeEventSourceFixture(t)
			test.mutate(t, directory, manifestPath)
			_, err := loadModel(manifestPath)
			if err == nil {
				t.Fatal("event-source manifest unexpectedly loaded")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

func eventSourceNamesSchema(schema map[string]any) map[string]any {
	events := schema["properties"].(map[string]any)["events"].(map[string]any)
	return events["properties"].(map[string]any)["names"].(map[string]any)
}

// TestLoadModelRejectsEventSupportWithoutEventSource keeps closed Entity types
// closed: only a manifest that declares event_source may declare support.events.
func TestLoadModelRejectsEventSupportWithoutEventSource(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeEventSourceFixture(t)
	patchManifestTopLevel(t, manifestPath, "event_source", false)
	writeJSON(t, filepath.Join(directory, "support.schema.json"), eventSourceSupportSchema())
	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), `unsupported top-level property "events"`) {
		t.Fatalf("closed type event support error = %v", err)
	}
}

// TestEventSourceRenderingOmitsObservationAndCommandArtifacts pins the
// non-commandable, stateless event-source surface: the generated facade, its
// conformance test, and the catalog expose only support, descriptor, and Entity
// Event name behavior.
func TestEventSourceRenderingOmitsObservationAndCommandArtifacts(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeEventSourceFixture(t)
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	facade := renderFacade(model, "example.test")
	facadeText := string(facade.content)
	for _, forbidden := range []string{
		"ObservationInput",
		"NewObservation",
		"type Handlers struct",
		"NewCommandHandler",
		"OperationSupport `json:",
	} {
		if strings.Contains(facadeText, forbidden) {
			t.Errorf("event-source facade contains %q:\n%s", forbidden, facadeText)
		}
	}
	for _, required := range []string{
		"func NewEntityEvent(input EntityEventInput) (adapter.EntityEvent, error)",
		"func NewEntityDescriptor(",
		"ValidateEntityEventName",
		"ValidateSupport(input.Support)",
	} {
		if !strings.Contains(facadeText, required) {
			t.Errorf("event-source facade omits %q:\n%s", required, facadeText)
		}
	}

	facadeTest := renderFacadeConformanceTest(model, "example.test")
	facadeTestText := string(facadeTest.content)
	for _, forbidden := range []string{
		"TestGeneratedObservationConformance",
		"TestGeneratedCommandConformance",
		"NewObservation(",
		"func requireValidationError",
	} {
		if strings.Contains(facadeTestText, forbidden) {
			t.Errorf("event-source facade test contains %q:\n%s", forbidden, facadeTestText)
		}
	}
	for _, required := range []string{
		"TestGeneratedEntityEventConformance",
		"adaptertest.RequireValidationError",
	} {
		if !strings.Contains(facadeTestText, required) {
			t.Errorf("event-source facade test omits %q:\n%s", required, facadeTestText)
		}
	}

	behavior := renderBehavior(model)
	behaviorText := string(behavior.content)
	for _, required := range []string{"func EntityEventNames(support Support) []string", "func ValidateEntityEventName("} {
		if !strings.Contains(behaviorText, required) {
			t.Errorf("event-source behavior omits %q:\n%s", required, behaviorText)
		}
	}
}

// TestEventSourceFacadeRunsSemanticSupportValidation protects the Entity Event
// builder's support check: schema encoding alone accepts relationally invalid
// support, so the generated builder must also call the generated semantic
// ValidateSupport, exactly as NewEntityDescriptor and NewCommandHandler do. It
// fails if the emitted builder only schema-encodes support, and it checks order
// so semantic rules cannot run against an undecoded value.
func TestEventSourceFacadeRunsSemanticSupportValidation(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeEventSourceFixture(t)
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	facade := renderFacade(model, "example.test")
	text := string(facade.content)
	encodeIndex := strings.Index(text, "codecs.Support.Encode(input.Support)")
	validateIndex := strings.Index(text, "contractfixtureeventv1.ValidateSupport(input.Support)")
	if encodeIndex < 0 {
		t.Fatalf("Entity Event builder does not schema-encode support:\n%s", text)
	}
	if validateIndex < 0 {
		t.Fatalf("Entity Event builder does not call semantic ValidateSupport:\n%s", text)
	}
	if validateIndex < encodeIndex {
		t.Fatalf("Entity Event builder validates semantics before schema encoding:\n%s", text)
	}
}

// TestEventSourceCatalogDefinitionUsesGeneratedSelector pins the catalog seam:
// the generator emits the selector wiring and Core keeps no handwritten event
// type branch.
func TestEventSourceCatalogDefinitionUsesGeneratedSelector(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeEventSourceFixture(t)
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog := renderCatalog([]entityTypeModel{model}, "example.test", t.TempDir())
	text := string(catalog.content)
	for _, required := range []string{
		"DefineEventSourceEntityType(",
		"contractfixtureeventv1.EntityEventNames,",
		"EntityTypeFixtureeventV1",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("generated catalog omits %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, "DefineOperation") {
		t.Errorf("generated catalog defines an operation for an event source:\n%s", text)
	}
	conformance, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conformanceText := string(conformance.content)
	for _, required := range []string{
		`catalog.SupportsEntityEvent(entity, EntityEventName("single_press"))`,
		`catalog.SupportsEntityEvent(entity, EntityEventName("unknown"))`,
		"catalog accepted a corrupt Entity Event descriptor",
		"catalog resolved a Command for an event source",
	} {
		if !strings.Contains(conformanceText, required) {
			t.Errorf("generated catalog conformance omits %q:\n%s", required, conformanceText)
		}
	}
}

// TestUnsupportedEntityEventNameAvoidsSupportedNames protects the generated
// rejection probes: the unsupported candidate must be absent from the complete
// supported name set.
func TestUnsupportedEntityEventNameAvoidsSupportedNames(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		names []string
		want  string
	}{
		{name: "empty", names: nil, want: "unknown"},
		{name: "unrelated", names: []string{"single_press"}, want: "unknown"},
		{name: "unknown taken", names: []string{"unknown"}, want: "unknown-event"},
		{name: "second taken", names: []string{"unknown", "unknown-event"}, want: "unsupported"},
		{
			name:  "all reserved",
			names: []string{"unknown", "unknown-event", "unsupported"},
			want:  "unknown-event-2",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := unsupportedEntityEventName(test.names)
			if got != test.want {
				t.Fatalf("candidate = %q, want %q", got, test.want)
			}
			for _, name := range test.names {
				if name == got {
					t.Fatalf("candidate %q collides with a supported name", got)
				}
			}
		})
	}
}

// TestSupportWithoutEventsBuildsCorruptDescriptor pins the corrupt-descriptor
// probe: removing events must preserve the remaining support members and must
// refuse a support that never carried events.
func TestSupportWithoutEventsBuildsCorruptDescriptor(t *testing.T) {
	t.Parallel()
	corrupt, err := supportWithoutEvents(json.RawMessage(
		`{"state":{},"operations":{},"events":{"names":["single_press"]}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(corrupt) != `{"operations":{},"state":{}}` {
		t.Fatalf("corrupt support = %s", corrupt)
	}
	if _, invalidErr := supportWithoutEvents(json.RawMessage(`{"state":{},"operations":{}}`)); invalidErr == nil {
		t.Fatal("support without events unexpectedly produced a corrupt descriptor")
	}
}

// TestSupportWithEventsBuildsClosedTypeProbe pins the closed-type probe: the
// added events member must survive marshaling so the generated assertion tests
// the closed support schema rather than a malformed value.
func TestSupportWithEventsBuildsClosedTypeProbe(t *testing.T) {
	t.Parallel()
	support, err := supportWithEvents(json.RawMessage(`{"state":{"maximum":10},"operations":{"set":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(support) != `{"events":{"names":["single_press"]},"operations":{"set":{}},"state":{"maximum":10}}` {
		t.Fatalf("closed-type probe support = %s", support)
	}
	if _, invalidErr := supportWithEvents(json.RawMessage(`not json`)); invalidErr == nil {
		t.Fatal("malformed support unexpectedly produced a probe")
	}
}

// TestRequireEventSourceExamplesRequiresNames keeps generated Entity Event
// probes possible: an authoring mistake must fail generation rather than emit a
// test without a name to compare.
func TestRequireEventSourceExamplesRequiresNames(t *testing.T) {
	t.Parallel()
	examples := examplesFile{Cases: []exampleCase{{
		Name:    "buttons",
		Support: json.RawMessage(`{"state":{},"operations":{},"events":{"names":[]}}`),
	}}}
	err := requireEventSourceExamples(examples)
	if err == nil || !strings.Contains(err.Error(), "support has no events.names") {
		t.Fatalf("examples error = %v", err)
	}
}
