//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
var prepareGetEntityOutput = sync.OnceValues(func() (*mcptools.OutputContract[EntityDetail], error) {
	return mcptools.PrepareOutput[EntityDetail](entityDetailSchema())
})

func registerGetEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetEntityInput, EntityDetail]{
			Name:        "get_entity",
			Description: "Inspect Entity support and full State and availability evidence.",
			Output:      mustContract(prepareGetEntityOutput()),
			Handler:     h.getEntity,
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

func entityDetailSchema() map[string]any {
	availability := recordSchema(map[string]any{
		"status": stringSchema(), "reason_code": stringSchema(), "source": stringSchema(),
		"since": stringSchema(), "evidence_at": stringSchema(), "source_observed_at": stringSchema(),
	}, "status", "source", "since", "evidence_at")
	state := recordSchema(map[string]any{
		"value": map[string]any{}, "observed_at": stringSchema(), "observation_id": stringSchema(),
		"adapter_received_at": stringSchema(), "source_updated_at": stringSchema(),
	}, "value", "observed_at", "observation_id", "adapter_received_at")
	return recordSchema(map[string]any{
		"entity_id": stringSchema(), "device_id": stringSchema(), "name": stringSchema(), "type": stringSchema(),
		"enabled": map[string]any{"type": "boolean"}, "adapter_id": stringSchema(),
		"support": map[string]any{"type": "object"}, "availability": availability, "state": nullableSchema(state),
	}, "entity_id", "device_id", "name", "type", "enabled", "adapter_id", "support", "availability", "state")
}

func (input *GetEntityInput) UnmarshalJSON(data []byte) error {
	type arguments GetEntityInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = GetEntityInput(decoded)
	return nil
}
