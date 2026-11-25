package mcpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
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

func deviceArea(area *devices.LabelRef) *LabelSummary {
	if area == nil {
		return nil
	}
	return &LabelSummary{LabelID: string(area.ID), Name: area.Name}
}

func entityArea(area *devices.EffectiveArea) *EntityArea {
	if area == nil {
		return nil
	}
	return &EntityArea{
		LabelID: string(area.Label.ID), Name: area.Label.Name, Source: string(area.Source),
	}
}

// labelSummaries is never nil: a target without tags reports an empty array.
func labelSummaries(refs []devices.LabelRef) []LabelSummary {
	result := make([]LabelSummary, len(refs))
	for index, ref := range refs {
		result[index] = LabelSummary{LabelID: string(ref.ID), Name: ref.Name}
	}
	return result
}

// nullableSchema is a property that is either null or the schema of T. The area
// is required on every read model, even when the target has none.
func nullableSchema[T any](registry huma.Registry) *huma.Schema {
	return &huma.Schema{AnyOf: []*huma.Schema{
		{Type: schemaTypeNull}, registry.Schema(reflect.TypeFor[T](), true, ""),
	}}
}
