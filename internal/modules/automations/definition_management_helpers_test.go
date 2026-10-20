package automations_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Catch devices/automations interface drift before application wiring.
var _ automations.AutomationDevices = (*devices.Service)(nil)

var errRuntimePersistenceUnavailable = errors.New("automation runtime persistence is not available in D1 tests")

type stubAutomationDevices struct {
	observationError  error
	conditionError    error
	entityEventError  error
	commandError      error
	normalizedCommand devices.CommandParameters
	observationCalls  []devices.EntityID
	conditionCalls    []devices.EntityID
	entityEventCalls  []string
	commandCalls      []devices.CommandInput
	observationValue  devices.Value
}

func (stub *stubAutomationDevices) ValidateObservationTrigger(
	_ context.Context, entityID devices.EntityID, pointers []string,
) error {
	stub.observationCalls = append(stub.observationCalls, entityID)
	if stub.observationError == nil && string(stub.observationValue) == "21600" {
		for _, pointer := range pointers {
			if pointer != "" {
				return fmt.Errorf(
					"%w: comparison pointer %q cannot select from the current Observation value; "+
						"value_pointer addresses the value directly, so use an empty value_pointer for a scalar and never /state/value",
					devices.ErrAutomationTriggerSource, pointer,
				)
			}
		}
	}
	return stub.observationError
}

func (stub *stubAutomationDevices) ValidateConditionEntity(_ context.Context, entityID devices.EntityID) error {
	stub.conditionCalls = append(stub.conditionCalls, entityID)
	return stub.conditionError
}

func (stub *stubAutomationDevices) GetEntityStateSnapshot(
	_ context.Context, entityIDs []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	value := stub.observationValue
	if value == nil {
		value = devices.Value(`{"temperature":21}`)
	}
	entries := make(map[devices.EntityID]devices.EntityStateSnapshotEntry, len(entityIDs))
	for _, entityID := range entityIDs {
		entries[entityID] = devices.EntityStateSnapshotEntry{
			EntityID: entityID,
			Exists:   true,
			State:    &devices.State{EntityID: entityID, Value: value},
		}
	}
	return devices.EntityStateSnapshot{Entries: entries}, nil
}

func (stub *stubAutomationDevices) ValidateEntityEventTrigger(
	_ context.Context, entityID devices.EntityID, name devices.EntityEventName,
) error {
	stub.entityEventCalls = append(stub.entityEventCalls, string(entityID)+"/"+string(name))
	return stub.entityEventError
}

func (stub *stubAutomationDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	stub.commandCalls = append(stub.commandCalls, input)
	if stub.commandError != nil {
		return nil, stub.commandError
	}
	if stub.normalizedCommand != nil {
		return stub.normalizedCommand, nil
	}
	return input.Parameters, nil
}

func (*stubAutomationDevices) CommandAdmissionOpen() bool { return true }

func (*stubAutomationDevices) ExecuteCommand(
	context.Context, devices.CommandInput,
) (devices.CommandResult, error) {
	return devices.CommandResult{}, errors.New("ExecuteCommand is not available in D1 tests")
}

func (*stubAutomationDevices) GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error) {
	return devices.CommandRecord{}, errors.New("GetCommand is not available in D1 tests")
}
