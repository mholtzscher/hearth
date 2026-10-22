//nolint:goconst // Literal schema types keep the operation's constraints local.
package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetEntityInput, EntityDetail]{
			Name:          "get_entity",
			Description:   "Inspect Entity support and full State and availability evidence.",
			SuccessSchema: entityDetailSchema(),
			Handler:       h.getEntity,
		},
	)
}

type GetEntityInput struct {
	EntityID entityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
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
	EntityID     string             `json:"entity_id"`
	DeviceID     string             `json:"device_id"`
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	Enabled      bool               `json:"enabled"`
	AdapterID    string             `json:"adapter_id"`
	Support      json.RawMessage    `json:"support"`
	Availability AvailabilityDetail `json:"availability"`
	State        *StateDetail       `json:"state"`
}

func (h *handler) getEntity(ctx context.Context, input GetEntityInput) (EntityDetail, error) {
	view, err := h.devices.GetEntity(ctx, devices.EntityID(input.EntityID))
	if err != nil {
		return EntityDetail{}, readFailure(err)
	}
	summary := entitySummary(view)
	result := EntityDetail{
		EntityID:  summary.EntityID,
		DeviceID:  summary.DeviceID,
		Name:      summary.Name,
		Type:      summary.Type,
		Enabled:   summary.Enabled,
		AdapterID: view.Entity.AdapterID,
		Support:   json.RawMessage(view.Entity.Support),
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

func entityDetailSchema() *jsonschema.Schema {
	schema := deriveResultSchema[EntityDetail]()
	schema.Properties["support"] = &jsonschema.Schema{Type: "object"}
	schema.Properties["availability"].Properties["source_observed_at"] = &jsonschema.Schema{Type: "string"}
	schema.Properties["state"].Properties["source_updated_at"] = &jsonschema.Schema{Type: "string"}
	return schema
}
