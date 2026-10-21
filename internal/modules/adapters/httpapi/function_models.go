//nolint:goconst // Literal JSON fields keep public schemas readable.
package httpapi

import (
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type FunctionBody struct {
	AdapterID       string          `json:"adapter_id"`
	FunctionKey     string          `json:"function_key"`
	CatalogRevision string          `json:"catalog_revision"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
	TimeoutMS       int             `json:"timeout_ms"`
	Callable        bool            `json:"callable"`
}

func functionCollectionSchema(items *huma.Schema) *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: false, Required: []string{"items"},
		Properties: map[string]*huma.Schema{"items": {Type: "array", Items: items}, "next_cursor": textSchema()}}
}

func functionBody(function adapters.Function) FunctionBody {
	return FunctionBody{AdapterID: function.AdapterID, FunctionKey: string(function.Descriptor.Key),
		CatalogRevision: string(function.CatalogRevision), Description: function.Descriptor.Description,
		InputSchema: function.Descriptor.InputSchema, OutputSchema: function.Descriptor.OutputSchema,
		TimeoutMS: function.Descriptor.TimeoutMS, Callable: function.Callable}
}

func functionNextCursor(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func objectSchema() *huma.Schema { return &huma.Schema{Type: "object"} }
func textSchema() *huma.Schema   { return &huma.Schema{Type: "string"} }

func (FunctionBody) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required: []string{
			"adapter_id",
			"function_key",
			"catalog_revision",
			"description",
			"input_schema",
			"output_schema",
			"timeout_ms",
			"callable",
		},
		Properties: map[string]*huma.Schema{"adapter_id": textSchema(), "function_key": textSchema(),
			"catalog_revision": textSchema(), "description": textSchema(), "input_schema": objectSchema(),
			"output_schema": objectSchema(), "timeout_ms": {Type: "integer"}, "callable": {Type: "boolean"}},
	}
}
