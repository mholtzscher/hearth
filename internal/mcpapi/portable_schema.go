package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// JSON Schema type names, so a table below reads as the specification rather
// than as a repeated string literal.
const (
	schemaTypeNull    = "null"
	schemaTypeBoolean = "boolean"
	schemaTypeObject  = "object"
	schemaTypeArray   = "array"
	schemaTypeNumber  = "number"
	schemaTypeString  = "string"
)

// Keyword names this package rewrites or reads by name, written once rather than
// repeated in prose.
const (
	schemaTypeKeyword         = "type"
	schemaAnyOfKeyword        = "anyOf"
	schemaItemsKeyword        = "items"
	schemaDependenciesKeyword = "dependencies"
)

// portableSchema rewrites one JSON Schema value into the shape every MCP client
// can read: no boolean subschema node and no array-valued "type".
//
// The SDK derives schemas with github.com/google/jsonschema-go, whose output is
// correct JSON Schema but not portable across MCP clients. Two shapes the
// Inspector flags in strict mode come from that derivation: an unconstrained Go
// value (any) derives the empty schema, which jsonschema-go marshals as the
// boolean `true` (and its negation as `false`), and a nullable Go value derives
// a `type` union such as `["null", "string"]` that a client reading `type` as a
// single string either rejects or drops.
//
// The rewrite preserves what validates: `true` becomes an explicit union of
// every JSON instance type and `false` an impossible null schema (the object
// forms avoid Inspector's untyped-schema warning without narrowing an
// unconstrained value to an object), and a `type` union becomes one `anyOf`
// branch per member type carrying the sibling constraints that apply to it.
//
// The result is a decoded JSON document rather than a *jsonschema.Schema,
// because jsonschema-go marshals an empty schema back to the boolean `true`; the
// wrapper hands the SDK a value it will not re-encode. The SDK remarshals the
// document for validation, where the explicit unions enforce exactly what the
// boolean schemas did.
//
// A schema that cannot be marshalled is returned unchanged: the SDK rejects the
// same value in AddTool, so its failure stays the single authoritative report.
func portableSchema(schema any) any {
	if schema == nil {
		return nil
	}
	normalized, err := normalizePortableSchema(schema)
	if err != nil {
		return schema
	}
	return normalized
}

// normalizePortableSchema marshals schema to JSON and rewrites the document as a
// decoded JSON object.
func normalizePortableSchema(schema any) (map[string]any, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	// UseNumber keeps numeric keywords (minimum, maxLength, an enum member)
	// byte-exact instead of rounding them through float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if decodeErr := decoder.Decode(&document); decodeErr != nil {
		return nil, fmt.Errorf("decode schema: %w", decodeErr)
	}
	normalized, ok := portableSchemaNode(document).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema root is %s, want a JSON object", reflect.TypeOf(document))
	}
	return normalized, nil
}

// portableSchemaNode rewrites one schema node in place and returns it. A boolean
// node accepts everything or nothing; an object is rewritten keyword by keyword;
// an array is the value of a list-of-schemas keyword, so it is rewritten element
// by element.
func portableSchemaNode(node any) any {
	switch value := node.(type) {
	case bool:
		return portableBooleanSchema(value)
	case map[string]any:
		return portableSchemaObject(value)
	case []any:
		for index, element := range value {
			value[index] = portableSchemaNode(element)
		}
		return value
	default:
		return node
	}
}

// portableBooleanSchema returns a typed object form of a boolean JSON Schema:
// the accept-all form enumerates every JSON instance type (Number includes
// integers, so these six branches cover the complete JSON data model), and the
// accept-none form requires null while rejecting it, which no JSON value can
// satisfy. Neither form contains the empty object schema strict MCP clients flag
// as untyped.
func portableBooleanSchema(acceptsEverything bool) map[string]any {
	if !acceptsEverything {
		return map[string]any{
			schemaTypeKeyword: schemaTypeNull,
			"not":             map[string]any{schemaTypeKeyword: schemaTypeNull},
		}
	}
	return map[string]any{schemaAnyOfKeyword: []any{
		map[string]any{schemaTypeKeyword: schemaTypeNull},
		map[string]any{schemaTypeKeyword: schemaTypeBoolean},
		map[string]any{schemaTypeKeyword: schemaTypeObject},
		map[string]any{schemaTypeKeyword: schemaTypeArray},
		map[string]any{schemaTypeKeyword: schemaTypeNumber},
		map[string]any{schemaTypeKeyword: schemaTypeString},
	}}
}

// portableSchemaObject rewrites one JSON Schema object: it recurses into every
// subschema the object holds, then replaces an array-valued "type" with an
// equivalent "anyOf".
func portableSchemaObject(node map[string]any) any {
	for keyword, value := range node {
		switch schemaKeywordShape(keyword) {
		case schemaValueSubschema:
			node[keyword] = portableSchemaNode(value)
		case schemaValueSubschemaArray:
			node[keyword] = portableSchemaSubschemaArray(value)
		case schemaValueSubschemaMap:
			node[keyword] = portableSchemaSubschemaMap(value)
		case schemaValueItems:
			node[keyword] = portableSchemaItems(value)
		case schemaValueDependencies:
			node[keyword] = portableSchemaDependencies(value)
		case schemaValueOther:
		}
	}
	memberTypes, isUnion := schemaTypeUnion(node[schemaTypeKeyword])
	if !isUnion {
		return node
	}
	return portableSchemaTypeUnion(node, memberTypes)
}

// portableSchemaTypeUnion replaces only the array-valued "type" assertion with a
// portable anyOf assertion and leaves every sibling at its original path, which
// preserves JSON Pointer targets, nested default traversal, $id/$ref behavior,
// and unknown extension keywords. Type-specific siblings remain
// semantics-equivalent because JSON Schema ignores them for instances of other
// types. A union with one member becomes that single type; an empty union
// accepts nothing.
func portableSchemaTypeUnion(node map[string]any, memberTypes []string) any {
	switch len(memberTypes) {
	case 0:
		return portableBooleanSchema(false)
	case 1:
		node[schemaTypeKeyword] = memberTypes[0]
		return node
	}
	delete(node, schemaTypeKeyword)
	branches := make([]any, 0, len(memberTypes))
	for _, memberType := range memberTypes {
		branches = append(branches, map[string]any{schemaTypeKeyword: memberType})
	}
	typeAssertion := map[string]any{schemaAnyOfKeyword: branches}
	if _, hasAnyOf := node[schemaAnyOfKeyword]; !hasAnyOf {
		node[schemaAnyOfKeyword] = branches
		return node
	}
	// A schema may already compose alternatives with anyOf. Keep that existing
	// constraint and add the portable type assertion through allOf instead of
	// replacing it and accidentally widening accepted arguments.
	if allOf, ok := node["allOf"].([]any); ok {
		node["allOf"] = append(allOf, typeAssertion)
	} else {
		node["allOf"] = []any{typeAssertion}
	}
	return node
}

// portableSchemaSubschemaArray rewrites a list-of-subschemas keyword such as
// "anyOf" or draft-07 tuple "items".
func portableSchemaSubschemaArray(value any) any {
	elements, ok := value.([]any)
	if !ok {
		return value
	}
	for index, element := range elements {
		elements[index] = portableSchemaNode(element)
	}
	return elements
}

// portableSchemaSubschemaMap rewrites a name-to-subschema keyword such as
// "properties" or "$defs".
func portableSchemaSubschemaMap(value any) any {
	entries, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for name, entry := range entries {
		entries[name] = portableSchemaNode(entry)
	}
	return entries
}

// portableSchemaItems rewrites "items", whose value is one subschema or, in
// draft-07, a list of tuple subschemas.
func portableSchemaItems(value any) any {
	switch value.(type) {
	case map[string]any, bool:
		return portableSchemaNode(value)
	case []any:
		return portableSchemaSubschemaArray(value)
	default:
		return value
	}
}

// portableSchemaDependencies rewrites draft-07 "dependencies", whose value maps a
// property name to a subschema or to a list of required property names. Only the
// subschema form is a schema.
func portableSchemaDependencies(value any) any {
	entries, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for name, entry := range entries {
		switch entry.(type) {
		case map[string]any, bool:
			entries[name] = portableSchemaNode(entry)
		}
	}
	return entries
}

// schemaValueShape is the shape of the value one JSON Schema keyword holds, which
// decides how the portability rewrite descends into it.
type schemaValueShape int

const (
	// schemaValueOther is a keyword holding a constant, an instance, a name, or a
	// list of names: nothing to rewrite.
	schemaValueOther schemaValueShape = iota
	// schemaValueSubschema is a keyword whose value is one subschema.
	schemaValueSubschema
	// schemaValueSubschemaArray is a keyword whose value is a list of subschemas.
	schemaValueSubschemaArray
	// schemaValueSubschemaMap is a keyword whose value maps a name to a subschema.
	schemaValueSubschemaMap
	// schemaValueItems is "items": one subschema, or a draft-07 list of them.
	schemaValueItems
	// schemaValueDependencies is draft-07 "dependencies": a subschema or a list of
	// property names per entry.
	schemaValueDependencies
)

// schemaKeywordShape classifies one JSON Schema keyword by the shape of its value
// for both draft-07 and draft 2020-12. It is deliberately a closed table rather
// than a search for subschema-looking values: a keyword like "enum" or "const"
// holds instance values, and rewriting a boolean instance there would change the
// accepted set.
func schemaKeywordShape(keyword string) schemaValueShape {
	switch keyword {
	case "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems",
		"propertyNames", "contains", "not", "if", "then", "else", "contentSchema":
		return schemaValueSubschema
	case "allOf", "anyOf", "oneOf", "prefixItems":
		return schemaValueSubschemaArray
	case "properties", "patternProperties", "dependentSchemas", "$defs", "definitions":
		return schemaValueSubschemaMap
	case schemaItemsKeyword:
		return schemaValueItems
	case schemaDependenciesKeyword:
		return schemaValueDependencies
	}
	return schemaValueOther
}

// schemaTypeUnion returns the member type names of an array-valued "type", and
// whether the value is one. A single-string "type" is already portable and
// reports false, as does a malformed union whose members are not all strings; a
// malformed union is reported to the caller by the SDK instead.
func schemaTypeUnion(value any) ([]string, bool) {
	members, isArray := value.([]any)
	if !isArray {
		return nil, false
	}
	memberTypes := make([]string, 0, len(members))
	for _, member := range members {
		name, isString := member.(string)
		if !isString {
			return nil, false
		}
		memberTypes = append(memberTypes, name)
	}
	return memberTypes, true
}
