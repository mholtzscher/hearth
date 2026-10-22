package mcpapi

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

type AvailabilitySummary struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type StateSummary struct {
	Value      json.RawMessage `json:"value"`
	ObservedAt string          `json:"observed_at"`
}

type EntitySummary struct {
	EntityID     string              `json:"entity_id"`
	DeviceID     string              `json:"device_id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Enabled      bool                `json:"enabled"`
	Availability AvailabilitySummary `json:"availability"`
	State        *StateSummary       `json:"state"`
}

func (EntitySummary) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	// State is required even when no accepted Observation has supplied it yet.
	schema.Properties["state"] = &huma.Schema{AnyOf: []*huma.Schema{
		{Type: "null"}, registry.Schema(reflect.TypeFor[StateSummary](), true, ""),
	}}
	return schema
}
