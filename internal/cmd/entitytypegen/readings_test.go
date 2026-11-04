package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	quantityReading := &readingManifest{Kind: readingKindQuantity, Quantity: "pressure"}
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
			want: &readingModel{Kind: readingKindQuantity, Quantity: "pressure", FixedUnit: "hPa"},
		},
		{
			name: "per-Entity unit from support", definition: manifest{Reading: quantityReading},
			state: number, support: readingSupport(freeUnit, true),
			want: &readingModel{Kind: readingKindQuantity, Quantity: "pressure", SupportUnitPath: supportUnitPath},
		},
		{
			name:       "manifest unit without support unit",
			definition: manifest{Reading: &readingManifest{Kind: readingKindQuantity, Quantity: "level", Unit: "%"}},
			state:      schemaNode{Type: string(kindInteger)}, support: readingSupport(nil, false),
			want: &readingModel{Kind: readingKindQuantity, Quantity: "level", FixedUnit: "%"},
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
			name: "unit from two sources",
			definition: manifest{
				Reading: &readingManifest{Kind: readingKindQuantity, Quantity: "pressure", Unit: "hPa"},
			},
			state:   number,
			support: readingSupport(fixedUnit, true),
			wantErr: "both the manifest",
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

func TestCheckReadingQuantitiesMatchesUnits(t *testing.T) {
	t.Parallel()
	table := quantities{
		"pressure": {
			DefaultDisplay: "hpa",
			Displays:       map[string]quantityDisplay{"hpa": {From: "hPa", Label: "hPa", Scale: 1}},
		},
		"generic": {
			DefaultDisplay: "native",
			Displays:       map[string]quantityDisplay{"native": {From: "*", Label: "{unit}", Scale: 1}},
		},
	}
	model := func(reading readingModel) []entityTypeModel {
		return []entityTypeModel{{TypeID: "example.value/v1", Reading: &reading}}
	}
	tests := []struct {
		name    string
		models  []entityTypeModel
		wantErr string
	}{
		{name: "fixed unit matches", models: model(readingModel{Quantity: "pressure", FixedUnit: "hPa"})},
		{
			name:   "per-Entity unit on generic",
			models: model(readingModel{Quantity: "generic", SupportUnitPath: supportUnitPath}),
		},
		{
			name:    "unknown quantity",
			models:  model(readingModel{Quantity: "volume", FixedUnit: "L"}),
			wantErr: "not in quantities.json",
		},
		{
			name:    "fixed unit mismatch",
			models:  model(readingModel{Quantity: "pressure", FixedUnit: "kPa"}),
			wantErr: `converts from "hPa"`,
		},
		{
			name:    "per-Entity unit on fixed quantity",
			models:  model(readingModel{Quantity: "pressure", SupportUnitPath: supportUnitPath}),
			wantErr: `unit is "*"`,
		},
		{
			name:    "fixed unit on generic",
			models:  model(readingModel{Quantity: "generic", FixedUnit: "hPa"}),
			wantErr: `converts from "*"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := checkReadingQuantities(test.models, table)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func writeQuantities(t *testing.T, root string, table map[string]any) {
	t.Helper()
	directory := filepath.Join(root, "contracts", "quantities", "v1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(
		filepath.Join(repositoryRoot(t), "contracts", "quantities", "v1", "quantities.schema.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "quantities.schema.json"), schema, 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "quantities.json"), table)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadQuantitiesRejectsInconsistentTables(t *testing.T) {
	t.Parallel()
	withReading := []entityTypeModel{{Reading: &readingModel{}}}
	display := func(label string) map[string]any {
		return map[string]any{"name": "Percent", "from": "%", "label": label, "scale": 1, "fraction_digits": 0}
	}
	tests := []struct {
		name    string
		table   map[string]any
		wantErr string
	}{
		{
			name: "shared key with different precision",
			table: map[string]any{
				"a": map[string]any{"default_display": "percent", "displays": map[string]any{"percent": display("%")}},
				"b": map[string]any{"default_display": "percent", "displays": map[string]any{
					"percent": map[string]any{
						"name":            "Percent",
						"from":            "%",
						"label":           "%",
						"scale":           1,
						"fraction_digits": 1,
					},
				}},
			},
		},
		{
			name: "shared key with different label",
			table: map[string]any{
				"a": map[string]any{"default_display": "percent", "displays": map[string]any{"percent": display("%")}},
				"b": map[string]any{
					"default_display": "percent",
					"displays":        map[string]any{"percent": display("pct")},
				},
			},
			wantErr: "different name or label",
		},
		{
			name: "default display missing",
			table: map[string]any{
				"a": map[string]any{"default_display": "other", "displays": map[string]any{"percent": display("%")}},
			},
			wantErr: "default_display",
		},
		{
			name: "schema violation",
			table: map[string]any{
				"a": map[string]any{"default_display": "percent", "displays": map[string]any{
					"percent": map[string]any{
						"name":            "Percent",
						"from":            "%",
						"label":           "%",
						"scale":           0,
						"fraction_digits": 0,
					},
				}},
			},
			wantErr: "validate quantities schema",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeQuantities(t, root, test.table)
			_, err := loadQuantities(root, withReading)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadQuantitiesSkipsRootsWithoutReadings(t *testing.T) {
	t.Parallel()
	table, err := loadQuantities(t.TempDir(), []entityTypeModel{{}})
	if err != nil || len(table) != 0 {
		t.Fatalf("table = %v, err = %v", table, err)
	}
}

func TestReadingsJSONIsOwnedAndChecked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	models := []entityTypeModel{{
		Package: "pressurev1", TypeID: "hearth.pressure/v1",
		Reading: &readingModel{Kind: readingKindQuantity, Quantity: "pressure", FixedUnit: "hPa"},
	}}
	table := quantities{"pressure": {
		DefaultDisplay: "hpa",
		Displays: map[string]quantityDisplay{
			"hpa": {Name: "Hectopascals", From: "hPa", Label: "hPa", Scale: 1, FractionDigits: 1},
		},
	}}
	_, jsonOutput, err := renderReadings(models, table, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonOutput.content), `"label": "hPa"`) ||
		!strings.Contains(string(jsonOutput.content), `"fixed": "hPa"`) {
		t.Fatalf("readings JSON = %s", jsonOutput.content)
	}
	outputs := []output{jsonOutput}
	if err = applyOutputs(root, outputs, true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing check error = %v", err)
	}
	if err = applyOutputs(root, outputs, false); err != nil {
		t.Fatal(err)
	}
	if err = applyOutputs(root, outputs, true); err != nil {
		t.Fatalf("fresh check error = %v", err)
	}
	if err = os.WriteFile(jsonOutput.path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = applyOutputs(root, outputs, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale check error = %v", err)
	}
	if err = applyOutputs(root, nil, true); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphan check error = %v", err)
	}
}
