//nolint:goconst // Literal schema types keep the operation's constraints local.
package mcpapi

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetEntityInput, EntityDetail]{
			Name:        "get_entity",
			Description: "Inspect Entity support, the parameter schema of each supported Command operation, and full State and availability evidence.",
			Handler:     h.getEntity,
		},
	)
}

type GetEntityInput struct {
	EntityID entityID `json:"entity_id"       doc:"canonical Hearth Entity ID"`
	Units    string   `json:"units,omitempty" doc:"Omit for household units"   enum:",canonical"`
}

type AvailabilityDetail struct {
	AvailabilitySummary

	Source           string  `json:"source"`
	Since            string  `json:"since"`
	EvidenceAt       string  `json:"evidence_at"`
	SourceObservedAt *string `json:"source_observed_at,omitempty"`
}

type StateDetail struct {
	StateSummary

	ObservationID     string  `json:"observation_id"`
	AdapterReceivedAt string  `json:"adapter_received_at"`
	SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
}

type EntityDetail struct {
	EntityID     string                              `json:"entity_id"`
	DeviceID     string                              `json:"device_id"`
	Name         string                              `json:"name"`
	Type         string                              `json:"type"`
	Enabled      bool                                `json:"enabled"`
	AdapterID    string                              `json:"adapter_id"`
	Support      json.RawMessage                     `json:"support"`
	Availability AvailabilityDetail                  `json:"availability"`
	State        *StateDetail                        `json:"state"`
	Area         *EntityArea                         `json:"area"`
	Tags         []LabelSummary                      `json:"tags"         nullable:"false"`
	Quantities   map[string]devices.QuantityMetadata `json:"quantities"`
	// Operations maps each supported operation to the JSON Schema its
	// execute_entity_command parameters must satisfy.
	Operations map[string]OperationDetail `json:"operations"`
}

type OperationDetail struct {
	Parameters json.RawMessage `json:"parameters"`
}

func (h *handler) getEntity(ctx context.Context, input GetEntityInput) (EntityDetail, error) {
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return EntityDetail{}, invalidRequestFailure("invalid units")
	}
	view, err := h.devices.GetEntity(ctx, devices.EntityID(input.EntityID))
	if err != nil {
		return EntityDetail{}, readFailure(err)
	}
	projected, quantities, err := h.presenter.Entity(view, units)
	if err != nil {
		return EntityDetail{}, mcptools.InternalToolError(err)
	}
	view = projected
	operations, err := entityOperations(h.devices, view.Entity)
	if err != nil {
		return EntityDetail{}, mcptools.InternalToolError(err)
	}
	summary := entitySummary(view, quantities)
	result := EntityDetail{
		Operations: operations,
		Quantities: quantities,
		EntityID:   summary.EntityID,
		DeviceID:   summary.DeviceID,
		Name:       summary.Name,
		Type:       summary.Type,
		Enabled:    summary.Enabled,
		Area:       summary.Area,
		Tags:       summary.Tags,
		AdapterID:  view.Entity.AdapterID,
		Support:    json.RawMessage(view.Entity.Support),
		Availability: AvailabilityDetail{
			AvailabilitySummary: summary.Availability,
			Source:              view.Availability.Source,
			Since:               formatTime(view.Availability.Since),
			EvidenceAt:          formatTime(view.Availability.EvidenceAt),
			SourceObservedAt:    optionalTime(view.Availability.SourceObservedAt),
		},
	}
	if view.State != nil {
		result.State = &StateDetail{
			StateSummary:      *summary.State,
			ObservationID:     string(view.State.ObservationID),
			AdapterReceivedAt: formatTime(view.State.AdapterReceivedAt),
			SourceUpdatedAt:   optionalTime(view.State.SourceUpdatedAt),
		}
	}
	return result, nil
}

// entityOperations projects each supported operation's parameter schema,
// dropping only the identification keys a caller does not need to build
// parameters.
func entityOperations(source Devices, entity devices.Entity) (map[string]OperationDetail, error) {
	contracts, err := source.EntityOperations(entity)
	if err != nil {
		return nil, err
	}
	operations := make(map[string]OperationDetail, len(contracts))
	for _, contract := range contracts {
		var schema map[string]json.RawMessage
		if err = json.Unmarshal(contract.ParametersSchema, &schema); err != nil {
			return nil, err
		}
		delete(schema, "$schema")
		delete(schema, "$id")
		delete(schema, "title")
		compact, marshalErr := json.Marshal(schema)
		if marshalErr != nil {
			return nil, marshalErr
		}
		operations[string(contract.Name)] = OperationDetail{Parameters: compact}
	}
	return operations, nil
}

func (EntityDetail) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["support"] = &huma.Schema{Type: "object"}
	schema.Properties["operations"] = &huma.Schema{Type: "object", AdditionalProperties: &huma.Schema{
		Type: "object", Required: []string{"parameters"}, AdditionalProperties: false,
		Properties: map[string]*huma.Schema{"parameters": {Type: "object"}},
	}}
	schema.Properties["area"] = nullableSchema[EntityArea](registry)
	schema.Properties["state"] = &huma.Schema{AnyOf: []*huma.Schema{
		{Type: schemaTypeNull}, registry.Schema(reflect.TypeFor[StateDetail](), true, ""),
	}}
	return schema
}
