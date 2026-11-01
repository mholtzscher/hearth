package mcpapi

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// CodeInvalidArguments classifies a shared argument-shape rejection. Module
// decoders retain their own more specific failure codes.
const CodeInvalidArguments = "invalid_arguments"

const invalidArgumentsMessage = "invalid tool arguments"
const argumentDiagnosticsField = "diagnostics"

type argumentDiagnostic struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Allowed []string `json:"allowed,omitempty"`
}

func invalidArguments(code string) *ToolError {
	return &ToolError{Code: CodeInvalidArguments, Message: invalidArgumentsMessage,
		Details: map[string]any{argumentDiagnosticsField: []argumentDiagnostic{{Code: code, Path: ""}}}}
}

func inputSchemaFailure(schema *jsonschema.Schema, instance any, err error) error {
	validation, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return err
	}
	// One safe leaf is sufficient. Do not expose validator messages or enumerate
	// every alternative of an arbitrary union as if they were domain diagnostics.
	for len(validation.Causes) > 0 {
		validation = validation.Causes[0]
	}
	path, location := safeArgumentPath(schema, instance, validation.InstanceLocation)
	diagnostic := argumentDiagnostic{Code: "constraint_violation", Path: path}
	if location != nil && strings.Count(path, "/") < len(validation.InstanceLocation) &&
		closedArgumentObject(location) {
		return &ToolError{
			Code:    CodeInvalidArguments,
			Message: invalidArgumentsMessage,
			Details: map[string]any{
				argumentDiagnosticsField: []argumentDiagnostic{{Code: "unknown_property", Path: path,
					Allowed: slices.Sorted(maps.Keys(location.Properties))}},
			},
		}
	}
	switch failure := validation.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		diagnostic.Code = "unknown_property"
		if location != nil {
			diagnostic.Allowed = slices.Sorted(maps.Keys(location.Properties))
		}
	case *kind.Required:
		diagnostic.Code = "missing_property"
		if location != nil && len(failure.Missing) > 0 && location.Properties[failure.Missing[0]] != nil {
			diagnostic.Path += "/" + escapePointer(failure.Missing[0])
		}
	case *kind.Type:
		diagnostic.Code = "type_mismatch"
	}
	return &ToolError{Code: CodeInvalidArguments, Message: invalidArgumentsMessage,
		Details: map[string]any{argumentDiagnosticsField: []argumentDiagnostic{diagnostic}}}
}

// safeArgumentPath follows only declared properties and array indexes. Stopping
// at an unresolved composition or arbitrary map is conservative but never echoes
// a submitted map key. Reference cycles stop within each traversal step.
func safeArgumentPath(schema *jsonschema.Schema, instance any, path []string) (string, *jsonschema.Schema) {
	var pointer strings.Builder
	for _, part := range path {
		schema = argumentReference(schema)
		if schema == nil {
			return pointer.String(), nil
		}
		var child *jsonschema.Schema
		switch value := instance.(type) {
		case map[string]any:
			child = schema.Properties[part]
			instance = value[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return pointer.String(), schema
			}
			instance = value[index]
			child = schema.Items2020
			if child == nil {
				child, _ = schema.Items.(*jsonschema.Schema)
			}
		}
		if child == nil {
			return pointer.String(), schema
		}
		pointer.WriteString("/" + escapePointer(part))
		schema = child
	}
	return pointer.String(), argumentReference(schema)
}

func closedArgumentObject(schema *jsonschema.Schema) bool {
	if allowed, ok := schema.AdditionalProperties.(bool); ok {
		return !allowed
	}
	additional, ok := schema.AdditionalProperties.(*jsonschema.Schema)
	// normalizePortableSchema represents false as null intersected with not-null.
	return ok && additional.Types != nil && additional.Not != nil && additional.Not.Types != nil &&
		slices.Equal(additional.Types.ToStrings(), []string{schemaTypeNull}) &&
		slices.Equal(additional.Not.Types.ToStrings(), []string{schemaTypeNull})
}

func argumentReference(schema *jsonschema.Schema) *jsonschema.Schema {
	seen := make(map[*jsonschema.Schema]bool)
	for schema != nil && schema.Ref != nil {
		if seen[schema] {
			return nil
		}
		seen[schema] = true
		schema = schema.Ref
	}
	return schema
}
