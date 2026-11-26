package mcpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

const schemaTypeNull = "null"

// LabelSummary names an assigned Label.
type LabelSummary struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
}

// EntityArea is an Entity's effective Area and whether it is the Entity's own
// or inherited from its Device.
type EntityArea struct {
	LabelSummary

	Source string `json:"source" enum:"device,entity"`
}

// LabelDetail is one catalog Label.
type LabelDetail struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"     enum:"area,tag"`
}

// nullableSchema is a property that is either null or the schema of T. The area
// is required on every read model, even when the target has none.
func nullableSchema[T any](registry huma.Registry) *huma.Schema {
	return &huma.Schema{AnyOf: []*huma.Schema{
		{Type: schemaTypeNull}, registry.Schema(reflect.TypeFor[T](), true, ""),
	}}
}
