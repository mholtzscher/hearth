package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func entityBody(view devices.EntityWithState) (EntityBody, error) {
	var support map[string]any
	if err := decodeJSON(view.Entity.Support, &support); err != nil {
		return EntityBody{}, fmt.Errorf("decode entity support: %w", err)
	}
	body := EntityBody{
		ID:       string(view.Entity.ID),
		DeviceID: string(view.Entity.DeviceID),
		Name:     view.Entity.Name,
		Type:     string(view.Entity.TypeID),
		Support:  support,
		Enabled:  view.Entity.Enabled,
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

func deviceBody(device devices.Device) DeviceBody {
	return DeviceBody{ID: string(device.ID), Kind: string(device.Kind), Name: device.Name}
}

func deviceDetailBody(aggregate devices.DeviceAggregate) (DeviceDetailBody, error) {
	body := DeviceDetailBody{
		ID: string(aggregate.Device.ID), Kind: string(aggregate.Device.Kind), Name: aggregate.Device.Name,
		Entities: make([]EntityBody, len(aggregate.Entities.Items)),
	}
	for index, entity := range aggregate.Entities.Items {
		mapped, err := entityBody(entity)
		if err != nil {
			return DeviceDetailBody{}, err
		}
		body.Entities[index] = mapped
	}
	return body, nil
}

func commandRecordBody(command devices.CommandRecord) (CommandRecordBody, error) {
	var parameters map[string]any
	if err := decodeJSON(command.Parameters, &parameters); err != nil {
		return CommandRecordBody{}, err
	}
	body := CommandRecordBody{
		ID: string(command.ID), EntityID: string(command.EntityID), Operation: string(command.OperationName),
		Parameters: parameters, Status: string(command.Status), RequestedAt: formatTime(command.RequestedAt),
		DeadlineAt: formatTime(command.DeadlineAt),
	}
	if command.AcceptedAt != nil {
		value := formatTime(*command.AcceptedAt)
		body.AcceptedAt = &value
	}
	if command.CompletedAt != nil {
		value := formatTime(*command.CompletedAt)
		body.CompletedAt = &value
	}
	if command.OutcomeObservationID != nil {
		value := string(*command.OutcomeObservationID)
		body.OutcomeObservationID = &value
	}
	if command.FailureCode != nil {
		value := string(*command.FailureCode)
		body.FailureCode = &value
	}
	return body, nil
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
