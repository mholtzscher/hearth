package scripted

import (
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/entitytypes"
	thermostat "github.com/mholtzscher/hearth/entitytypes/thermostatv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func validateEntityCommand(entity *scriptedEntity, command adapter.Command) error {
	if entity.codecs.TypeID != thermostat.TypeID {
		return nil
	}
	return validateThermostatCommand(entity.support, command.OperationName, command.Parameters)
}

// validateThermostatParameters uses the same schema and support rules as Core
// and the typed SDK, including operation presence.
func validateThermostatParameters[P, O any](
	codec *entitytypes.JSONCodec[P], raw json.RawMessage, support thermostat.Support,
	operation *O, validate func(thermostat.Support, O, P) error,
) error {
	if operation == nil {
		return errors.New("thermostat operation is not supported")
	}
	parameters, _, err := codec.Decode(raw)
	if err != nil {
		return err
	}
	return validate(support, *operation, parameters)
}

func validateThermostatCommand(rawSupport json.RawMessage, operation string, parameters json.RawMessage) error {
	codecs, err := thermostat.Compile()
	if err != nil {
		return err
	}
	support, _, err := codecs.Support.Decode(rawSupport)
	if err != nil {
		return err
	}
	switch operation {
	case thermostat.OperationSetTarget:
		return validateThermostatParameters(codecs.SetTargetParameters, parameters, support,
			support.Operations.SetTarget, thermostat.ValidateSetTargetParameters)
	case thermostat.OperationSetRange:
		return validateThermostatParameters(codecs.SetRangeParameters, parameters, support,
			support.Operations.SetRange, thermostat.ValidateSetRangeParameters)
	case thermostat.OperationSetMode:
		return validateThermostatParameters(codecs.SetModeParameters, parameters, support,
			support.Operations.SetMode, thermostat.ValidateSetModeParameters)
	case thermostat.OperationSetFanMode:
		return validateThermostatParameters(codecs.SetFanModeParameters, parameters, support,
			support.Operations.SetFanMode, thermostat.ValidateSetFanModeParameters)
	case thermostat.OperationSetHumidity:
		return validateThermostatParameters(codecs.SetHumidityParameters, parameters, support,
			support.Operations.SetHumidity, thermostat.ValidateSetHumidityParameters)
	default:
		return errors.New("unknown thermostat operation")
	}
}

// applyThermostatParametersLocked merges validated parameters into the matching
// State fields without disturbing other reported values. The caller holds the mutex.
func applyThermostatParametersLocked(entity *scriptedEntity, operation string, parameters json.RawMessage) {
	var state, params map[string]json.RawMessage
	if json.Unmarshal(entity.current, &state) != nil || json.Unmarshal(parameters, &params) != nil {
		return
	}
	switch operation {
	case thermostat.OperationSetTarget:
		state["target_temperature"] = params["temperature"]
	case thermostat.OperationSetRange:
		state["target_temperature_low"] = params["low"]
		state["target_temperature_high"] = params["high"]
	case thermostat.OperationSetMode:
		state["mode"] = params["mode"]
	case thermostat.OperationSetFanMode:
		state["fan_mode"] = params["fan_mode"]
	case thermostat.OperationSetHumidity:
		state["target_humidity"] = params["humidity"]
	default:
		return
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return
	}
	if normalized, normalizeErr := entity.codecs.NormalizeState(raw); normalizeErr == nil {
		entity.current = normalized
	}
}
