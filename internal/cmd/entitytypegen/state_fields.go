package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var fieldTokenPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type stateFieldModel struct {
	Path      string         `json:"-"`
	ValueType string         `json:"value_type"`
	Presence  string         `json:"presence"`
	Enum      []string       `json:"enum,omitempty"`
	Quantity  *quantityModel `json:"quantity,omitempty"`
}

type quantityModel struct {
	Unit         any  `json:"unit"` // A fixed string or a validated support reference.
	UnitOptional bool `json:"unit_optional,omitempty"`
}

type fieldAnnotation struct {
	Unit json.RawMessage `json:"unit"`
}

func stateFieldsJSONPath(root string) string {
	return filepath.Join(root, "contracts", "state-fields", "v1", "state-fields.json")
}

//nolint:gocognit // The recursive traversal follows concrete schema nodes in field order.
func deriveStateFields(root string, state, support schemaNode, stateless bool) ([]stateFieldModel, error) {
	if stateless {
		if hasFieldAnnotation(state) {
			return nil, errors.New("stateless type cannot declare hearth annotations")
		}
		return nil, nil
	}
	annotationSchema, compileErr := compileAnnotationSchema(root)
	if compileErr != nil {
		return nil, compileErr
	}
	var fields []stateFieldModel
	var walk func(schemaNode, string, bool) error
	walk = func(node schemaNode, path string, always bool) error {
		if err := validateFieldComposition(node); err != nil {
			return fmt.Errorf("field %q: %w", path, err)
		}
		numeric := node.Type == string(kindInteger) || node.Type == string(kindNumber)
		if len(node.Hearth) > 0 && !numeric {
			return fmt.Errorf("field %q: hearth requires a numeric scalar", path)
		}
		switch node.Type {
		case schemaTypeObject:
			for _, name := range sortedProperties(node.Properties) {
				next := path + "/" + name
				if !fieldTokenPattern.MatchString(name) || len(next) > 64 {
					return fmt.Errorf("field %q: unsupported State pointer", next)
				}
				if err := walk(node.Properties[name], next, always && stateFieldRequired(node, name)); err != nil {
					return err
				}
			}
			return nil
		case schemaTypeArray:
			if node.Items != nil && hasFieldAnnotation(*node.Items) {
				return fmt.Errorf("field %q: annotated array paths are unsupported", path)
			}
			return nil
		case string(kindInteger), string(kindNumber), string(kindBoolean), string(kindString):
		default:
			return fmt.Errorf("field %q: unsupported scalar type %q", path, node.Type)
		}
		presence := "sometimes"
		if always {
			presence = "always"
		}
		field := stateFieldModel{Path: path, ValueType: node.Type, Presence: presence}
		if node.Type == string(kindString) && len(node.Enum) > 0 {
			if enumErr := json.Unmarshal(node.Enum, &field.Enum); enumErr != nil {
				return fmt.Errorf("field %q: string enum: %w", path, enumErr)
			}
		}
		if numeric {
			quantity, err := deriveQuantity(node.Hearth, support, annotationSchema)
			if err != nil {
				return fmt.Errorf("field %q: %w", path, err)
			}
			field.Quantity = quantity
		}
		fields = append(fields, field)
		return nil
	}
	if err := walk(state, "", true); err != nil {
		return nil, err
	}
	return fields, nil
}

func compileAnnotationSchema(root string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(filepath.Join(root, "entitytypes", "state-field-annotations.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("read hearth annotation schema: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	const id = "urn:hearth:schema:state-field-annotations:v1"
	if addErr := compiler.AddResource(id, value); addErr != nil {
		return nil, addErr
	}
	return compiler.Compile(id)
}

// oneOf may restrict requiredness and discriminator constants, never redefine
// fields. The concrete field definitions stay at their parent object.
func validateFieldComposition(node schemaNode) error {
	if err := rejectUnsupportedAnnotations(node); err != nil {
		return err
	}
	if node.Ref != "" || len(node.AllOf) > 0 || len(node.AnyOf) > 0 || node.If != nil || node.Then != nil ||
		node.Else != nil ||
		node.Not != nil {
		return errors.New("unsupported semantic schema composition")
	}
	for _, branch := range node.OneOf {
		if err := rejectUnsupportedAnnotations(branch); err != nil {
			return err
		}
		expectedBranch := schemaNode{Required: branch.Required, Not: branch.Not, Properties: branch.Properties}
		gotBranch, _ := json.Marshal(branch)
		wantBranch, _ := json.Marshal(expectedBranch)
		if !bytes.Equal(gotBranch, wantBranch) {
			return errors.New("unsupported oneOf restriction")
		}
		if node.Type != schemaTypeObject {
			return errors.New("ambiguous oneOf field definitions")
		}
		if err := validateFieldBranch(node, branch); err != nil {
			return err
		}
	}
	return nil
}

func validateFieldBranch(node, branch schemaNode) error {
	for name, restriction := range branch.Properties {
		if err := rejectUnsupportedAnnotations(restriction); err != nil {
			return err
		}
		original, exists := node.Properties[name]
		if !exists || original.Type != string(kindString) || len(restriction.Const) == 0 {
			return fmt.Errorf("ambiguous oneOf property %q", name)
		}
		// Only a discriminator const is supported, not branch-local type/units.
		encoded, _ := json.Marshal(restriction)
		var empty schemaNode
		empty.Const = restriction.Const
		expected, _ := json.Marshal(empty)
		if !bytes.Equal(encoded, expected) {
			return fmt.Errorf("conflicting oneOf property %q", name)
		}
	}
	for _, name := range branch.Required {
		if _, exists := node.Properties[name]; !exists {
			return fmt.Errorf("unresolved oneOf required property %q", name)
		}
	}
	if branch.Not != nil {
		onlyRequired := schemaNode{Required: branch.Not.Required}
		got, _ := json.Marshal(branch.Not)
		want, _ := json.Marshal(onlyRequired)
		if !bytes.Equal(got, want) {
			return errors.New("unsupported oneOf exclusion")
		}
		for _, name := range branch.Not.Required {
			if _, exists := node.Properties[name]; !exists {
				return fmt.Errorf("unresolved oneOf excluded property %q", name)
			}
		}
	}
	return nil
}

func stateFieldRequired(node schemaNode, name string) bool {
	if required(node, name) {
		return true
	}
	if len(node.OneOf) == 0 {
		return false
	}
	for _, branch := range node.OneOf {
		if !required(branch, name) {
			return false
		}
	}
	return true
}

// Unknown schema keywords may be ordinary JSON Schema metadata, but cannot hide
// semantic declarations outside the concrete field traversal we support.
func rejectUnsupportedAnnotations(node schemaNode) error {
	if len(node.Raw) == 0 {
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(node.Raw, &members); err != nil {
		return err
	}
	for key, raw := range members {
		switch key {
		case "hearth", "properties", "items", "oneOf", "allOf", "anyOf", "not", "if", "then", "else":
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if containsAnnotation(value) {
			return fmt.Errorf("unsupported hearth declaration under %q", key)
		}
	}
	return nil
}

func containsAnnotation(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, exists := value["hearth"]; exists {
			return true
		}
		for _, child := range value {
			if containsAnnotation(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(value, containsAnnotation)
	}
	return false
}

func hasFieldAnnotation(node schemaNode) bool {
	if len(node.Raw) > 0 {
		var value any
		if err := json.Unmarshal(node.Raw, &value); err == nil && containsAnnotation(value) {
			return true
		}
	}
	if len(node.Hearth) > 0 {
		return true
	}
	for _, child := range node.Properties {
		if hasFieldAnnotation(child) {
			return true
		}
	}
	if node.Items != nil && hasFieldAnnotation(*node.Items) {
		return true
	}
	for _, branches := range [][]schemaNode{node.OneOf, node.AllOf, node.AnyOf} {
		if slices.ContainsFunc(branches, hasFieldAnnotation) {
			return true
		}
	}
	return false
}

func deriveQuantity(
	raw json.RawMessage,
	support schemaNode,
	annotationSchema *jsonschema.Schema,
) (*quantityModel, error) {
	quantity := &quantityModel{Unit: "1"}
	if len(raw) == 0 {
		return quantity, nil
	}
	value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if decodeErr != nil {
		return nil, decodeErr
	}
	if err := annotationSchema.Validate(value); err != nil {
		return nil, fmt.Errorf("invalid hearth annotation: %w", err)
	}
	var annotation fieldAnnotation
	if err := json.Unmarshal(raw, &annotation); err != nil {
		return nil, err
	}
	unit, optional, unitErr := deriveUnitSource(annotation.Unit, support)
	if unitErr != nil {
		return nil, unitErr
	}
	quantity.Unit = unit
	quantity.UnitOptional = optional
	return quantity, nil
}

func deriveUnitSource(raw json.RawMessage, support schemaNode) (any, bool, error) {
	if len(raw) == 0 {
		return "1", false, nil
	}
	var fixed string
	if err := json.Unmarshal(raw, &fixed); err == nil {
		return fixed, false, nil
	}
	var reference referenceManifest
	if err := json.Unmarshal(raw, &reference); err != nil {
		return nil, false, err
	}
	target, always, err := resolveInterpretationReference(support, reference)
	if err != nil {
		return nil, false, err
	}
	if target.Type != string(kindString) {
		return nil, false, errors.New("unit reference must target a string support property")
	}
	return reference, !always, nil
}

func resolveInterpretationReference(support schemaNode, reference referenceManifest) (schemaNode, bool, error) {
	if reference.Root != "support" || reference.Path == "" || len(reference.Path) > 64 {
		return schemaNode{}, false, errors.New("invalid support interpretation reference")
	}
	node, always := support, true
	if err := validateFieldComposition(node); err != nil {
		return schemaNode{}, false, fmt.Errorf("support reference %q: %w", reference.Path, err)
	}
	for token := range strings.SplitSeq(strings.TrimPrefix(reference.Path, "/"), "/") {
		if !strings.HasPrefix(reference.Path, "/") || !fieldTokenPattern.MatchString(token) ||
			node.Type != schemaTypeObject {
			return schemaNode{}, false, fmt.Errorf("invalid support reference %q", reference.Path)
		}
		always = always && stateFieldRequired(node, token)
		child, exists := node.Properties[token]
		if !exists {
			return schemaNode{}, false, fmt.Errorf("unresolved support reference %q", reference.Path)
		}
		if err := validateFieldComposition(child); err != nil {
			return schemaNode{}, false, fmt.Errorf("support reference %q: %w", reference.Path, err)
		}
		node = child
	}
	return node, always, nil
}

func stateFieldOutputs(root string, models []entityTypeModel) ([]output, error) {
	document := struct {
		Comment string                                `json:"$comment"`
		Types   map[string]map[string]stateFieldModel `json:"types"`
	}{Comment: strings.TrimSuffix(strings.TrimPrefix(generatedPrefix, "// "), "\n"), Types: map[string]map[string]stateFieldModel{}}
	ordered := append([]entityTypeModel(nil), models...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TypeID < ordered[j].TypeID })
	var source, references strings.Builder
	generatedHeader(&source)
	source.WriteString(
		"package devices\n\n//nolint:gochecknoglobals // Generated immutable lookup.\nvar builtinStateFields = map[EntityTypeID][]StateField{\n",
	)
	references.WriteString(
		"//nolint:gochecknoglobals // Generated immutable interpretation references.\nvar builtinInterpretationReferences = map[EntityTypeID][]interpretationReference{\n",
	)
	for _, model := range ordered {
		fields := map[string]stateFieldModel{}
		fmt.Fprintf(&source, "EntityType%s: {\n", entityTypeGoName(model))
		immutable := map[string]string{}
		for _, field := range model.StateFields {
			fields[field.Path] = field
			renderStateField(&source, field, immutable)
		}
		source.WriteString("},\n")
		if len(fields) > 0 {
			document.Types[model.TypeID] = fields
		}
		if len(immutable) > 0 {
			fmt.Fprintf(&references, "EntityType%s: {", entityTypeGoName(model))
			paths := make([]string, 0, len(immutable))
			for path := range immutable {
				paths = append(paths, path)
			}
			sort.Strings(paths)
			for _, path := range paths {
				references.WriteString(immutable[path] + ",")
			}
			references.WriteString("},\n")
		}
	}
	source.WriteString("}\n\n")
	source.WriteString(references.String() + "}\n")
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode State fields: %w", err)
	}
	return []output{
		{
			path:    filepath.Join(root, "internal", "modules", "devices", "zz_generated_entitytypes_state_fields.go"),
			content: []byte(source.String()),
		},
		{path: stateFieldsJSONPath(root), content: encoded.Bytes()},
	}, nil
}

func renderStateField(source *strings.Builder, field stateFieldModel, immutable map[string]string) {
	fmt.Fprintf(source, "{Path: %q, Presence: %q, Value: ", field.Path, field.Presence)
	switch field.ValueType {
	case string(kindBoolean):
		source.WriteString("BooleanField{}")
	case string(kindString):
		fmt.Fprintf(source, "StringField{Enum: %#v}", field.Enum)
	default:
		quantity := field.Quantity
		fmt.Fprintf(source, "NumericField{ValueType: %q, Unit: ", field.ValueType)
		switch unit := quantity.Unit.(type) {
		case string:
			fmt.Fprintf(source, "FixedUnit{Code: %q}", unit)
		case referenceManifest:
			fmt.Fprintf(
				source,
				"SupportUnit{Reference: SupportReference{Path: %q}, Optional: %t}",
				unit.Path,
				quantity.UnitOptional,
			)
			immutable[unit.Path] = fmt.Sprintf("{Path: %q, Optional: %t}", unit.Path, quantity.UnitOptional)
		}
		source.WriteString("}")
	}
	source.WriteString("},\n")
}
