package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileObjectContract prepares an owned object contract without external
// loading. Input preparation also uses its compiler to validate default values.
func compileObjectContract(uri string, raw json.RawMessage) (*jsonschema.Compiler, *jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("decode schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(blockExternalSchemas{})
	if err = compiler.AddResource(uri, document); err != nil {
		return nil, nil, fmt.Errorf("load schema: %w", err)
	}
	contract, err := compiler.Compile(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("compile schema: %w", err)
	}
	if !schemaAllowsObject(contract, nil) {
		return nil, nil, fmt.Errorf("schema cannot accept an object")
	}
	return compiler, contract, nil
}

// schemaAllowsObject rejects explicit non-object root types through references
// and composition. It is a type check, not a general schema satisfiability proof;
// recursive references add no new restriction once already visited.
func schemaAllowsObject(schema *jsonschema.Schema, seen map[*jsonschema.Schema]bool) bool {
	if schema == nil || seen[schema] {
		return true
	}
	if (schema.Bool != nil && !*schema.Bool) ||
		(schema.Types != nil && !slices.Contains(schema.Types.ToStrings(), schemaTypeObject)) {
		return false
	}
	if seen == nil {
		seen = make(map[*jsonschema.Schema]bool)
	}
	seen[schema] = true
	defer delete(seen, schema)
	if !schemaAllowsObject(schema.Ref, seen) {
		return false
	}
	for _, child := range schema.AllOf {
		if !schemaAllowsObject(child, seen) {
			return false
		}
	}
	for _, alternatives := range [][]*jsonschema.Schema{schema.AnyOf, schema.OneOf} {
		if len(alternatives) > 0 && !slices.ContainsFunc(alternatives, func(child *jsonschema.Schema) bool {
			return schemaAllowsObject(child, seen)
		}) {
			return false
		}
	}
	return true
}

type blockExternalSchemas struct{}

func (blockExternalSchemas) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema loading disabled: %s", url)
}
