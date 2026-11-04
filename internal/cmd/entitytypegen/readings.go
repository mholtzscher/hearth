package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	readingKindQuantity = "quantity"
	perEntityUnit       = "*"
	supportUnitPath     = "/state/unit"
	quantitiesSchemaID  = "urn:hearth:schema:quantities:v1"
)

type readingManifest struct {
	Kind     string `json:"kind"`
	Quantity string `json:"quantity"`
	Unit     string `json:"unit,omitempty"`
}

// readingModel is one manifest reading after its unit source is resolved
// against the State and support schemas. FixedUnit and SupportUnitPath are
// mutually exclusive.
type readingModel struct {
	Kind            string
	Quantity        string
	FixedUnit       string
	SupportUnitPath string
}

type quantityDisplay struct {
	Name           string  `json:"name"`
	From           string  `json:"from"`
	Label          string  `json:"label"`
	Scale          float64 `json:"scale"`
	Offset         float64 `json:"offset"`
	FractionDigits int     `json:"fraction_digits"`
}

type quantity struct {
	DefaultDisplay string                     `json:"default_display"`
	Displays       map[string]quantityDisplay `json:"displays"`
}

type quantities map[string]quantity

func quantitiesPath(root string) string {
	return filepath.Join(root, "contracts", "quantities", "v1", "quantities.json")
}

func readingsJSONPath(root string) string {
	return filepath.Join(root, "contracts", "readings", "v1", "readings.json")
}

func readingsGoPath(root string) string {
	return filepath.Join(root, "internal", "modules", "devices", "zz_generated_entitytypes_readings.go")
}

// resolveReading checks a manifest reading against the State and support
// schemas. It returns nil when the manifest declares no reading.
func resolveReading(definition manifest, state, stateSupport schemaNode) (*readingModel, error) {
	declared := definition.Reading
	if declared == nil {
		return nil, nil //nolint:nilnil // No reading is a valid, distinct outcome.
	}
	if declared.Kind != readingKindQuantity {
		return nil, fmt.Errorf("reading kind %q is unsupported", declared.Kind)
	}
	if state.Type != string(kindNumber) && state.Type != string(kindInteger) {
		return nil, fmt.Errorf("quantity reading requires a number or integer State, not %q", state.Type)
	}
	if definition.Stateless || definition.EventSource {
		return nil, errors.New("quantity reading requires a stateful, non-event-source type")
	}
	model := &readingModel{Kind: declared.Kind, Quantity: declared.Quantity}
	unit, supportDeclaresUnit := stateSupport.Properties["unit"]
	switch {
	case supportDeclaresUnit && declared.Unit != "":
		return nil, errors.New("reading unit is declared by both the manifest and support.state.unit")
	case supportDeclaresUnit:
		if unit.Type != string(kindString) {
			return nil, errors.New("support.state.unit must be a string")
		}
		if unit.Const == nil {
			if !required(stateSupport, "unit") {
				return nil, errors.New("a per-Entity reading unit must be required in support.state")
			}
			model.SupportUnitPath = supportUnitPath
			break
		}
		if err := json.Unmarshal(unit.Const, &model.FixedUnit); err != nil || model.FixedUnit == "" {
			return nil, errors.New("support.state.unit const must be a non-empty string")
		}
	case declared.Unit != "":
		model.FixedUnit = declared.Unit
	default:
		return nil, errors.New("reading requires a unit from support.state.unit or the manifest")
	}
	return model, nil
}

// loadQuantities reads the hand-written quantity table when any model declares
// a reading; roots without readings need no quantity contract.
func loadQuantities(root string, models []entityTypeModel) (quantities, error) {
	needed := false
	for _, model := range models {
		needed = needed || model.Reading != nil
	}
	if !needed {
		return quantities{}, nil
	}
	path := quantitiesPath(root)
	if err := validateQuantitiesSchema(path); err != nil {
		return nil, err
	}
	var table quantities
	if err := decodeStrictFile(path, &table); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	labels := map[string]string{}
	for _, name := range sortedKeys(table) {
		entry := table[name]
		if _, ok := entry.Displays[entry.DefaultDisplay]; !ok {
			return nil, fmt.Errorf(
				"quantity %q default_display %q is not one of its displays",
				name,
				entry.DefaultDisplay,
			)
		}
		for _, key := range sortedKeys(entry.Displays) {
			display := entry.Displays[key]
			identity := display.Name + "\x00" + display.Label
			if previous, seen := labels[key]; seen && previous != identity {
				return nil, fmt.Errorf("display %q has a different name or label in quantity %q", key, name)
			}
			labels[key] = identity
		}
	}
	return table, nil
}

func validateQuantitiesSchema(path string) error {
	schemaPath := filepath.Join(filepath.Dir(path), "quantities.schema.json")
	rawSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read quantities schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawSchema))
	if err != nil {
		return fmt.Errorf("decode quantities schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource(quantitiesSchemaID, document); err != nil {
		return fmt.Errorf("add quantities schema: %w", err)
	}
	compiled, err := compiler.Compile(quantitiesSchemaID)
	if err != nil {
		return fmt.Errorf("compile quantities schema: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read quantities: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode quantities: %w", err)
	}
	if err = compiled.Validate(value); err != nil {
		return fmt.Errorf("validate quantities schema: %w", err)
	}
	return nil
}

// checkReadingQuantities confirms each reading names a known quantity whose
// displays convert from the reading's canonical unit.
func checkReadingQuantities(models []entityTypeModel, table quantities) error {
	for _, model := range models {
		reading := model.Reading
		if reading == nil {
			continue
		}
		entry, ok := table[reading.Quantity]
		if !ok {
			return fmt.Errorf("%s: reading quantity %q is not in quantities.json", model.TypeID, reading.Quantity)
		}
		want := reading.FixedUnit
		if reading.SupportUnitPath != "" {
			want = perEntityUnit
		}
		for _, key := range sortedKeys(entry.Displays) {
			if from := entry.Displays[key].From; from != want {
				return fmt.Errorf(
					"%s: quantity %q display %q converts from %q, but the reading unit is %q",
					model.TypeID, reading.Quantity, key, from, want,
				)
			}
		}
	}
	return nil
}

type readingsDocument struct {
	Comment string                    `json:"$comment"`
	Types   map[string]readingsEntity `json:"types"`
}

type readingsEntity struct {
	Kind           string                     `json:"kind"`
	Quantity       string                     `json:"quantity"`
	Unit           readingsUnit               `json:"unit"`
	DefaultDisplay string                     `json:"default_display"`
	Displays       map[string]readingsDisplay `json:"displays"`
}

type readingsUnit struct {
	Fixed       string `json:"fixed,omitempty"`
	SupportPath string `json:"support_path,omitempty"`
}

type readingsDisplay struct {
	Name           string  `json:"name"`
	Label          string  `json:"label"`
	Scale          float64 `json:"scale"`
	Offset         float64 `json:"offset"`
	FractionDigits int     `json:"fraction_digits"`
}

// readingOutputs loads the quantity table, checks every reading against it and
// renders the Go lookup and JSON contract.
func readingOutputs(root string, models []entityTypeModel) ([]output, error) {
	table, err := loadQuantities(root, models)
	if err != nil {
		return nil, err
	}
	if err = checkReadingQuantities(models, table); err != nil {
		return nil, err
	}
	goFile, jsonFile, err := renderReadings(models, table, root)
	if err != nil {
		return nil, err
	}
	return []output{goFile, jsonFile}, nil
}

// renderReadings emits the Go lookup and the JSON contract from the same models.
func renderReadings(models []entityTypeModel, table quantities, root string) (output, output, error) {
	ordered := make([]entityTypeModel, 0, len(models))
	for _, model := range models {
		if model.Reading != nil {
			ordered = append(ordered, model)
		}
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].TypeID < ordered[right].TypeID })

	document := readingsDocument{
		Comment: strings.TrimSuffix(strings.TrimPrefix(generatedPrefix, "// "), "\n"),
		Types:   make(map[string]readingsEntity, len(ordered)),
	}
	var source strings.Builder
	generatedHeader(&source)
	source.WriteString("package devices\n\n")
	source.WriteString("//nolint:gochecknoglobals // Generated immutable lookup.\n")
	source.WriteString("var builtinReadings = map[EntityTypeID]Reading{\n")
	for _, model := range ordered {
		reading := model.Reading
		entry := table[reading.Quantity]
		keys := sortedKeys(entry.Displays)
		displays := make(map[string]readingsDisplay, len(keys))
		quoted := make([]string, 0, len(keys))
		for _, key := range keys {
			display := entry.Displays[key]
			displays[key] = readingsDisplay{
				Name:           display.Name,
				Label:          display.Label,
				Scale:          display.Scale,
				Offset:         display.Offset,
				FractionDigits: display.FractionDigits,
			}
			quoted = append(quoted, strconv.Quote(key))
		}
		document.Types[model.TypeID] = readingsEntity{
			Kind: reading.Kind, Quantity: reading.Quantity,
			Unit:           readingsUnit{Fixed: reading.FixedUnit, SupportPath: reading.SupportUnitPath},
			DefaultDisplay: entry.DefaultDisplay, Displays: displays,
		}
		fmt.Fprintf(&source, "\tEntityType%s: {\n", entityTypeGoName(model))
		fmt.Fprintf(&source, "\t\tKind: ReadingQuantity, Quantity: %s,\n", strconv.Quote(reading.Quantity))
		if reading.FixedUnit != "" {
			fmt.Fprintf(&source, "\t\tFixedUnit: %s,\n", strconv.Quote(reading.FixedUnit))
		} else {
			fmt.Fprintf(&source, "\t\tSupportUnitPath: %s,\n", strconv.Quote(reading.SupportUnitPath))
		}
		fmt.Fprintf(&source, "\t\tDefaultDisplay: %s,\n", strconv.Quote(entry.DefaultDisplay))
		fmt.Fprintf(&source, "\t\tDisplays: []string{%s},\n", strings.Join(quoted, ", "))
		source.WriteString("\t},\n")
	}
	source.WriteString("}\n\n")
	source.WriteString("// BuiltinReading returns the reading a built-in Entity type declares.\n")
	source.WriteString("func BuiltinReading(id EntityTypeID) (Reading, bool) {\n")
	source.WriteString("\treading, ok := builtinReadings[id]\n\treturn reading, ok\n}\n")

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return output{}, output{}, fmt.Errorf("encode readings: %w", err)
	}
	return output{path: readingsGoPath(root), content: []byte(source.String())},
		output{path: readingsJSONPath(root), content: encoded.Bytes()},
		nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
