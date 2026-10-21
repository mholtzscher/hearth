package httpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
