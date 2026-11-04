package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	readingKindQuantity = "quantity"
	supportUnitPath     = "/state/unit"
)

type readingManifest struct {
	Kind string `json:"kind"`
	Unit string `json:"unit,omitempty"`
}

// readingModel is one manifest reading after its unit source is resolved
// against the State and support schemas. FixedUnit and SupportUnitPath are
// mutually exclusive; units are UCUM codes by convention, which the web units
// module interprets.
type readingModel struct {
	Kind            string
	ValueType       string
	FixedUnit       string
	SupportUnitPath string
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
	model := &readingModel{Kind: declared.Kind, ValueType: state.Type}
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

type readingsDocument struct {
	Comment string                    `json:"$comment"`
	Types   map[string]readingsEntity `json:"types"`
}

type readingsEntity struct {
	Kind      string       `json:"kind"`
	ValueType string       `json:"value_type"`
	Unit      readingsUnit `json:"unit"`
}

type readingsUnit struct {
	Fixed       string `json:"fixed,omitempty"`
	SupportPath string `json:"support_path,omitempty"`
}

// readingOutputs renders the Go lookup and the JSON contract from the same
// models.
func readingOutputs(root string, models []entityTypeModel) ([]output, error) {
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
		document.Types[model.TypeID] = readingsEntity{
			Kind: reading.Kind, ValueType: reading.ValueType,
			Unit: readingsUnit{Fixed: reading.FixedUnit, SupportPath: reading.SupportUnitPath},
		}
		fmt.Fprintf(&source, "\tEntityType%s: {\n", entityTypeGoName(model))
		fmt.Fprintf(&source, "\t\tKind: ReadingQuantity, ValueType: %s,\n", strconv.Quote(reading.ValueType))
		if reading.FixedUnit != "" {
			fmt.Fprintf(&source, "\t\tFixedUnit: %s,\n", strconv.Quote(reading.FixedUnit))
		} else {
			fmt.Fprintf(&source, "\t\tSupportUnitPath: %s,\n", strconv.Quote(reading.SupportUnitPath))
		}
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
		return nil, fmt.Errorf("encode readings: %w", err)
	}
	return []output{
		{path: readingsGoPath(root), content: []byte(source.String())},
		{path: readingsJSONPath(root), content: encoded.Bytes()},
	}, nil
}
