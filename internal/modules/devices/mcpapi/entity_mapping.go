package mcpapi

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (h *handler) entitySummary(view devices.EntityWithState, units devices.ReadUnits) (EntitySummary, error) {
	view, quantities, err := h.presenter.Entity(view, units)
	if err != nil {
		return EntitySummary{}, err
	}
	return entitySummary(view, quantities), nil
}

func entitySummary(view devices.EntityWithState, quantities map[string]devices.QuantityMetadata) EntitySummary {
	result := EntitySummary{
		Quantities: quantities,
		EntityID:   string(view.Entity.ID), DeviceID: string(view.Entity.DeviceID),
		Name: view.Entity.Name, Type: string(view.Entity.TypeID), Enabled: view.Entity.Enabled,
		Availability: AvailabilitySummary{Status: string(view.Availability.Status)},
	}
	if view.Availability.Reason != nil {
		result.Availability.ReasonCode = view.Availability.Reason.Code
	}
	if view.State != nil {
		result.State = &StateSummary{
			Value:      json.RawMessage(view.State.Value),
			ObservedAt: formatTime(view.State.ObservedAt),
		}
	}
	return result
}
