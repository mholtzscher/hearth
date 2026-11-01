package automations

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// schemaIssues explains one strict-schema failure. A kind-selected union
// reports only its value's own kind, rather than one failure per alternative;
// diagnostics name structure and allowed kinds but never echo submitted values.
func (codec *DefinitionCodec) schemaIssues(validation *jsonschema.ValidationError, document any) []DefinitionIssue {
	var issues []DefinitionIssue
	codec.collectSchemaIssues(validation, document, &issues)
	return issues
}

func (codec *DefinitionCodec) collectSchemaIssues(
	validation *jsonschema.ValidationError,
	document any,
	issues *[]DefinitionIssue,
) {
	if union, ok := validation.ErrorKind.(*kind.OneOf); ok && union.Subschemas == nil {
		// When no alternative matches, the validator reports one cause per
		// alternative in schema order, so the value's kind selects its cause.
		kinds := codec.unionKinds(validation.SchemaURL)
		if kinds != nil && len(kinds) == len(validation.Causes) {
			codec.collectKindIssues(validation, kinds, document, issues)
			return
		}
	}
	if len(validation.Causes) > 0 {
		for _, cause := range validation.Causes {
			codec.collectSchemaIssues(cause, document, issues)
		}
		return
	}
	location := validation.InstanceLocation
	switch failure := validation.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		// The rejected names are caller input; name the schema's properties instead.
		issue := schemaIssue(location, "unknown_property", "object has a property that is not allowed")
		issue.Allowed = codec.declaredProperties(validation.SchemaURL)
		*issues = append(*issues, issue)
	case *kind.Required:
		for _, property := range failure.Missing {
			*issues = append(*issues, schemaIssue(
				append(slices.Clone(location), property), "missing_property", "required property is missing",
			))
		}
	default:
		*issues = append(
			*issues,
			schemaIssue(location, "invalid_definition", "value does not satisfy the strict schema"),
		)
	}
}

func (codec *DefinitionCodec) collectKindIssues(
	validation *jsonschema.ValidationError,
	kinds []string,
	document any,
	issues *[]DefinitionIssue,
) {
	location := validation.InstanceLocation
	object, ok := instanceAt(document, location).(map[string]any)
	if !ok {
		*issues = append(
			*issues,
			schemaIssue(location, "invalid_definition", "value does not satisfy the strict schema"),
		)
		return
	}
	kindLocation := append(slices.Clone(location), "kind")
	name, present := object["kind"]
	if !present {
		issue := schemaIssue(kindLocation, "missing_property", "required property is missing")
		issue.Allowed = slices.Clone(kinds)
		*issues = append(*issues, issue)
		return
	}
	text, _ := name.(string)
	index := slices.Index(kinds, text)
	if index < 0 {
		issue := schemaIssue(kindLocation, "unknown_kind", "kind is not supported")
		issue.Allowed = slices.Clone(kinds)
		*issues = append(*issues, issue)
		return
	}
	codec.collectSchemaIssues(validation.Causes[index], document, issues)
}

// unionKinds returns the kind of each oneOf alternative at a compiled schema
// location, in schema order, or nil when the union is not selected by kind.
func (codec *DefinitionCodec) unionKinds(schemaURL string) []string {
	alternatives, _ := codec.schemaObject(schemaURL)["oneOf"].([]any)
	kinds := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		object, _ := alternative.(map[string]any)
		if ref, ok := object["$ref"].(string); ok && strings.HasPrefix(ref, "#") {
			object, _ = schemaNodeAt(codec.schemaDocument, strings.TrimPrefix(ref, "#")).(map[string]any)
		}
		properties, _ := object["properties"].(map[string]any)
		discriminator, _ := properties["kind"].(map[string]any)
		name, ok := discriminator["const"].(string)
		if !ok {
			return nil
		}
		kinds = append(kinds, name)
	}
	if len(kinds) == 0 {
		return nil
	}
	return kinds
}

// declaredProperties lists the property names of the canonical schema object
// at a compiled location, sorted for stable diagnostics.
func (codec *DefinitionCodec) declaredProperties(schemaURL string) []string {
	properties, _ := codec.schemaObject(schemaURL)["properties"].(map[string]any)
	if len(properties) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(properties))
}

func (codec *DefinitionCodec) schemaObject(schemaURL string) map[string]any {
	_, fragment, found := strings.Cut(schemaURL, "#")
	if !found {
		return nil
	}
	object, _ := schemaNodeAt(codec.schemaDocument, fragment).(map[string]any)
	return object
}

func schemaIssue(location []string, code, message string) DefinitionIssue {
	return DefinitionIssue{Path: definitionPointer(location), Message: message, Code: code}
}

// definitionPointer encodes an instance location as a JSON Pointer. Schema
// traversal may enter a variable-name map; invalid names must not become
// response paths, so the pointer stops at the containing map instead.
func definitionPointer(location []string) string {
	var pointer strings.Builder
	for _, part := range location {
		if strings.HasSuffix(pointer.String(), "/variables") ||
			strings.HasSuffix(pointer.String(), "/variables_expr") ||
			strings.HasSuffix(pointer.String(), "/assignments") ||
			strings.HasSuffix(pointer.String(), "/assignments_expr") {
			if !validVariableName(VariableName(part)) {
				break
			}
		}
		pointer.WriteString("/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return pointer.String()
}

func instanceAt(document any, location []string) any {
	value := document
	for _, part := range location {
		switch container := value.(type) {
		case map[string]any:
			value = container[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(container) {
				return nil
			}
			value = container[index]
		default:
			return nil
		}
	}
	return value
}

func schemaNodeAt(document any, pointer string) any {
	if pointer == "" {
		return document
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for index, part := range parts {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return instanceAt(document, parts)
}
