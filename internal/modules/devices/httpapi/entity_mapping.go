package httpapi

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func entityBody(view devices.EntityWithState) (EntityBody, error) {
	var support map[string]any
	if err := decodeJSON(view.Entity.Support, &support); err != nil {
		return EntityBody{}, fmt.Errorf("decode entity support: %w", err)
	}
	body := EntityBody{
		AdapterName: view.Entity.AdapterName, NameOverride: view.Entity.NameOverride,
		ID:           string(view.Entity.ID),
		DeviceID:     string(view.Entity.DeviceID),
		AdapterID:    view.Entity.AdapterID,
		Name:         view.Entity.Name,
		Type:         string(view.Entity.TypeID),
		Support:      support,
		Enabled:      view.Entity.Enabled,
		Availability: availabilityBody(view.Availability),
	}
	if view.State == nil {
		return body, nil
	}
	var value any
	if err := decodeJSON(view.State.Value, &value); err != nil {
		return EntityBody{}, fmt.Errorf("decode entity state: %w", err)
	}
	state := &StateBody{
		Value:             value,
		ObservationID:     string(view.State.ObservationID),
		AdapterReceivedAt: formatTime(view.State.AdapterReceivedAt),
		ObservedAt:        formatTime(view.State.ObservedAt),
	}
	if view.State.SourceUpdatedAt != nil {
		sourceUpdatedAt := formatTime(*view.State.SourceUpdatedAt)
		state.SourceUpdatedAt = &sourceUpdatedAt
	}
	body.State = state
	return body, nil
}

func availabilityBody(availability devices.EntityAvailability) AvailabilityBody {
	body := AvailabilityBody{
		Status: string(availability.Status), Source: availability.Source,
		Since: formatTime(availability.Since), EvidenceAt: formatTime(availability.EvidenceAt),
		Reason: healthReasonBody(availability.Reason),
	}
	if availability.SourceObservedAt != nil {
		value := formatTime(*availability.SourceObservedAt)
		body.SourceObservedAt = &value
	}
	return body
}
