package nativeapi

import (
	"errors"
	"math"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

// These flags are defined in ESPHome 2026.10.2 climate/climate_mode.h, not api.proto.
const (
	climateCurrentTemperature uint32 = 1 << iota
	climateTwoPointTarget
	climateRequiresTwoPoint
	climateCurrentHumidity
	climateTargetHumidity
	climateActivity
)

func decodeClimateInfo(message *apipb.ListEntitiesClimateResponse) *ClimateInfo {
	flags := message.GetFeatureFlags()
	info := &ClimateInfo{
		CurrentTemperature: message.GetSupportsCurrentTemperature() || flags&climateCurrentTemperature != 0,
		TwoPointTarget: message.GetSupportsTwoPointTargetTemperature() ||
			flags&(climateTwoPointTarget|climateRequiresTwoPoint) != 0,
		RequiresTwoPoint:      flags&climateRequiresTwoPoint != 0,
		CurrentHumidity:       message.GetSupportsCurrentHumidity() || flags&climateCurrentHumidity != 0,
		TargetHumidity:        message.GetSupportsTargetHumidity() || flags&climateTargetHumidity != 0,
		Activity:              message.GetSupportsAction() || flags&climateActivity != 0,
		MinimumTemperature:    shortestFloat(message.GetVisualMinTemperature()),
		MaximumTemperature:    shortestFloat(message.GetVisualMaxTemperature()),
		TargetTemperatureStep: shortestFloat(message.GetVisualTargetTemperatureStep()),
		MinimumHumidity:       shortestFloat(message.GetVisualMinHumidity()),
		MaximumHumidity:       shortestFloat(message.GetVisualMaxHumidity()),
		CustomFanModes:        message.GetSupportedCustomFanModes(),
	}
	switch message.GetTemperatureUnit() {
	case apipb.TemperatureUnit_TEMPERATURE_UNIT_CELSIUS:
		info.TemperatureUnit = "Cel"
	case apipb.TemperatureUnit_TEMPERATURE_UNIT_FAHRENHEIT:
		info.TemperatureUnit = "[degF]"
	case apipb.TemperatureUnit_TEMPERATURE_UNIT_KELVIN:
		info.TemperatureUnit = "K"
	}
	for _, mode := range message.GetSupportedModes() {
		info.Modes = append(info.Modes, climateEnumName(mode.String(), "CLIMATE_MODE_"))
	}
	for _, mode := range message.GetSupportedFanModes() {
		info.StandardFanModes = append(info.StandardFanModes, climateEnumName(mode.String(), "CLIMATE_FAN_"))
	}
	return info
}

func decodeClimateState(data []byte) (StateUpdate, bool, error) {
	var message apipb.ClimateStateResponse
	if err := decodeInto(data, &message); err != nil {
		return StateUpdate{}, false, err
	}
	state := &ClimateState{
		Mode:                  climateEnumName(message.GetMode().String(), "CLIMATE_MODE_"),
		CurrentTemperature:    finiteReading(message.GetCurrentTemperature()),
		TargetTemperature:     finiteReading(message.GetTargetTemperature()),
		TargetTemperatureLow:  finiteReading(message.GetTargetTemperatureLow()),
		TargetTemperatureHigh: finiteReading(message.GetTargetTemperatureHigh()),
		CurrentHumidity:       finiteReading(message.GetCurrentHumidity()),
		TargetHumidity:        finiteReading(message.GetTargetHumidity()),
	}
	if activity := climateEnumName(message.GetAction().String(), "CLIMATE_ACTION_"); activity != "" {
		state.Activity = &activity
	}
	// Proto3 cannot distinguish unset optional fan State from the zero-valued ON enum.
	if fan := climateEnumName(message.GetFanMode().String(), "CLIMATE_FAN_"); fan != "" &&
		message.GetFanMode() != apipb.ClimateFanMode_CLIMATE_FAN_ON {
		state.StandardFanMode = &fan
	}
	if custom := message.GetCustomFanMode(); custom != "" {
		state.CustomFanMode = &custom
	}
	return StateUpdate{
		Type: EntityClimate, Key: message.GetKey(), DeviceID: message.GetDeviceId(),
		Missing: message.GetMissingState(), Climate: state,
	}, true, nil
}

// climateEnumName uses the generated enum vocabulary; unknown numeric values have no name.
func climateEnumName(name, prefix string) string {
	value, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return ""
	}
	return strings.ToLower(value)
}

// IsStandardClimateFanMode matches ESPHome's case-insensitive standard-first fan parser.
func IsStandardClimateFanMode(name string) bool {
	_, exists := apipb.ClimateFanMode_value["CLIMATE_FAN_"+strings.ToUpper(name)]
	return exists
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func finiteReading(value float32) *float64 {
	converted := shortestFloat(value)
	if !finite(converted) {
		return nil
	}
	return &converted
}

func climateCommandMessage(command ClimateCommand) (proto.Message, error) {
	request := &apipb.ClimateCommandRequest{Key: command.Key, DeviceId: command.DeviceID}
	families := 0
	if command.Mode != nil {
		value, ok := apipb.ClimateMode_value["CLIMATE_MODE_"+strings.ToUpper(*command.Mode)]
		if !ok {
			return nil, errors.New("esphome: unknown climate mode")
		}
		request.HasMode, request.Mode = true, apipb.ClimateMode(value)
		families++
	}
	if command.StandardFanMode != nil {
		value, ok := apipb.ClimateFanMode_value["CLIMATE_FAN_"+strings.ToUpper(*command.StandardFanMode)]
		if !ok {
			return nil, errors.New("esphome: unknown climate fan mode")
		}
		request.HasFanMode, request.FanMode = true, apipb.ClimateFanMode(value)
		families++
	}
	if command.CustomFanMode != nil {
		if IsStandardClimateFanMode(*command.CustomFanMode) {
			return nil, errors.New("esphome: custom fan name resolves to a standard fan mode")
		}
		request.HasCustomFanMode, request.CustomFanMode = true, *command.CustomFanMode
		families++
	}
	temperatureFamilies, err := populateClimateTemperature(command, request)
	if err != nil {
		return nil, err
	}
	families += temperatureFamilies
	if command.TargetHumidity != nil {
		if !finite(*command.TargetHumidity) {
			return nil, errors.New("esphome: humidity target must be finite")
		}
		request.HasTargetHumidity, request.TargetHumidity = true, float32(*command.TargetHumidity)
		families++
	}
	if families != 1 {
		return nil, errors.New("esphome: climate command requires exactly one control family")
	}
	return request, nil
}

func populateClimateTemperature(command ClimateCommand, request *apipb.ClimateCommandRequest) (int, error) {
	families := 0
	if command.TargetTemperature != nil {
		if !finite(*command.TargetTemperature) {
			return 0, errors.New("esphome: climate target must be finite")
		}
		request.HasTargetTemperature, request.TargetTemperature = true, float32(*command.TargetTemperature)
		families++
	}
	if command.TargetTemperatureLow != nil || command.TargetTemperatureHigh != nil {
		if command.TargetTemperatureLow == nil || command.TargetTemperatureHigh == nil ||
			!finite(*command.TargetTemperatureLow) || !finite(*command.TargetTemperatureHigh) ||
			*command.TargetTemperatureLow > *command.TargetTemperatureHigh {
			return 0, errors.New("esphome: climate range requires two ordered finite targets")
		}
		request.HasTargetTemperatureLow, request.TargetTemperatureLow = true, float32(*command.TargetTemperatureLow)
		request.HasTargetTemperatureHigh, request.TargetTemperatureHigh = true, float32(*command.TargetTemperatureHigh)
		families++
	}
	return families, nil
}
