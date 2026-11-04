package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readingSupport(unit *schemaNode, requireUnit bool) schemaNode {
	support := schemaNode{Type: schemaTypeObject, Properties: map[string]schemaNode{}}
	if unit != nil {
		support.Properties["unit"] = *unit
		if requireUnit {
			support.Required = []string{"unit"}
		}
	}
	return support
}

func TestResolveReadingAppliesUnitAndStateRules(t *testing.T) {
	t.Parallel()
	quantityReading := &readingManifest{Kind: readingKindQuantity}
	fixedUnit := &schemaNode{Type: string(kindString), Const: json.RawMessage(`"hPa"`)}
	freeUnit := &schemaNode{Type: string(kindString)}
	number := schemaNode{Type: string(kindNumber)}
	tests := []struct {
		name       string
		definition manifest
		state      schemaNode
		support    schemaNode
		want       *readingModel
		wantErr    string
	}{
		{name: "no reading", definition: manifest{}, state: number, support: readingSupport(nil, false)},
		{
			name: "fixed unit from support const", definition: manifest{Reading: quantityReading},
			state: number, support: readingSupport(fixedUnit, true),
			want: &readingModel{Kind: readingKindQuantity, ValueType: "number", FixedUnit: "hPa"},
		},
		{
			name: "per-Entity unit from support", definition: manifest{Reading: quantityReading},
			state: number, support: readingSupport(freeUnit, true),
			want: &readingModel{Kind: readingKindQuantity, ValueType: "number", SupportUnitPath: supportUnitPath},
		},
		{
			name:       "manifest unit without support unit",
			definition: manifest{Reading: &readingManifest{Kind: readingKindQuantity, Unit: "%"}},
			state:      schemaNode{Type: string(kindInteger)}, support: readingSupport(nil, false),
			want: &readingModel{Kind: readingKindQuantity, ValueType: "integer", FixedUnit: "%"},
		},
		{
			name: "boolean State", definition: manifest{Reading: quantityReading},
			state: schemaNode{Type: string(kindBoolean)}, support: readingSupport(fixedUnit, true),
			wantErr: "number or integer State",
		},
		{
			name: "object State", definition: manifest{Reading: quantityReading},
			state: schemaNode{Type: schemaTypeObject}, support: readingSupport(fixedUnit, true),
			wantErr: "number or integer State",
		},
		{
			name: "stateless type", definition: manifest{Reading: quantityReading, Stateless: true},
			state: number, support: readingSupport(fixedUnit, true), wantErr: "stateful",
		},
		{
			name: "missing unit", definition: manifest{Reading: quantityReading},
			state: number, support: readingSupport(nil, false), wantErr: "requires a unit",
		},
		{
			name:       "unit from two sources",
			definition: manifest{Reading: &readingManifest{Kind: readingKindQuantity, Unit: "hPa"}},
			state:      number, support: readingSupport(fixedUnit, true), wantErr: "both the manifest",
		},
		{
			name: "optional per-Entity unit", definition: manifest{Reading: quantityReading},
			state: number, support: readingSupport(freeUnit, false), wantErr: "must be required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveReading(test.definition, test.state, test.support)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (test.want == nil) || (got != nil && *got != *test.want) {
				t.Fatalf("reading = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestReadingsJSONIsOwnedAndChecked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	models := []entityTypeModel{{
		Package: "pressurev1", TypeID: "hearth.pressure/v1",
		Reading: &readingModel{Kind: readingKindQuantity, ValueType: "number", FixedUnit: "hPa"},
	}}
	outputs, err := readingOutputs(root, models)
	if err != nil {
		t.Fatal(err)
	}
	jsonOutput := outputs[1]
	if !strings.Contains(string(jsonOutput.content), `"value_type": "number"`) ||
		!strings.Contains(string(jsonOutput.content), `"fixed": "hPa"`) {
		t.Fatalf("readings JSON = %s", jsonOutput.content)
	}
	only := []output{jsonOutput}
	if err = applyOutputs(root, only, true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing check error = %v", err)
	}
	if err = applyOutputs(root, only, false); err != nil {
		t.Fatal(err)
	}
	if err = applyOutputs(root, only, true); err != nil {
		t.Fatalf("fresh check error = %v", err)
	}
	if err = os.WriteFile(jsonOutput.path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = applyOutputs(root, only, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale check error = %v", err)
	}
	if err = applyOutputs(root, nil, true); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphan check error = %v", err)
	}
}
