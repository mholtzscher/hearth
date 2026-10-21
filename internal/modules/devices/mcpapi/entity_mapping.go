package mcpapi

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func entitySummary(view devices.EntityWithState) EntitySummary {
	result := EntitySummary{
		EntityID: string(view.Entity.ID), DeviceID: string(view.Entity.DeviceID),
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
