package httpapi

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (h *handler) entityBody(view devices.EntityWithState, units devices.ReadUnits) (EntityBody, error) {
	view, quantities, err := h.presenter.Entity(view, units)
	if err != nil {
		return EntityBody{}, err
	}
	var support map[string]any
	if decodeErr := decodeJSON(view.Entity.Support, &support); decodeErr != nil {
		return EntityBody{}, fmt.Errorf("decode entity support: %w", decodeErr)
	}
	body := EntityBody{
		Quantities:  quantities,
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
	if decodeErr := decodeJSON(view.State.Value, &value); decodeErr != nil {
		return EntityBody{}, fmt.Errorf("decode entity state: %w", decodeErr)
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
