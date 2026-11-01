package mcpapi

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// prepareInputSchema derives one owned portable input document from I and its
// native Huma schema hooks. Discovery and validation use that same document.
func prepareInputSchema[I any]() (json.RawMessage, error) {
	schema, err := deriveToolSchema(reflect.TypeFor[I]())
	if err != nil {
		return nil, fmt.Errorf("derive input schema: %w", err)
	}
	document, err := normalizePortableSchema(schema)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	return raw, nil
}
