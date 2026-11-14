package esphome

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/entitytypes"
	contractthermostatv1 "github.com/mholtzscher/hearth/entitytypes/thermostatv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkthermostatv1 "github.com/mholtzscher/hearth/sdk/adapter/thermostatv1"
)

const (
	minimumMilliTemperature    = -273150
	maximumMilliTemperature    = 1000000
	milliPerDegree             = 1000
	fahrenheitOffset           = 32
	fahrenheitRatioNumerator   = 9
	fahrenheitRatioDenominator = 5
	fahrenheitScale            = float64(fahrenheitRatioNumerator) / fahrenheitRatioDenominator
	kelvinOffsetHundredths     = 27315
	hundredthsPerDegree        = 100
	kelvinOffset               = float64(kelvinOffsetHundredths) / hundredthsPerDegree
	maximumHumidity            = 100
	thermostatCelsius          = "Cel"
	thermostatFahrenheit       = "[degF]"
	thermostatKelvin           = "K"
)

func (planner *entityPlanner) planThermostat(
	name, identity string, upstream upstreamRef, entity nativeapi.EntityInfo,
) error {
	if entity.Climate == nil ||
		!slices.Contains(
			[]string{thermostatCelsius, thermostatFahrenheit, thermostatKelvin},
			entity.Climate.TemperatureUnit,
		) {
		planner.skipCapability(entity, skipCapabilityUnsupported)
		return nil
	}
	info := entity.Climate
	support := sdkthermostatv1.Support{
		State: sdkthermostatv1.StateSupport{
			Modes: slices.Clone(info.Modes), FanModes: thermostatFanModes(info),
			TemperatureMinimum: minimumMilliTemperature, TemperatureMaximum: maximumMilliTemperature,
			HumidityMinimum: 0, HumidityMaximum: maximumHumidity,
		},
		Operations: sdkthermostatv1.OperationSupport{SetMode: &sdkthermostatv1.SetModeSupport{}},
	}
	if len(info.StandardFanModes)+len(info.CustomFanModes) > 0 {
		if len(support.State.FanModes) > 0 {
			support.Operations.SetFanMode = &sdkthermostatv1.SetFanModeSupport{}
		} else {
			planner.skipCapability(entity, "fan_capability_unsupported")
		}
	}
	if slices.Contains(info.StandardFanModes, "on") {
		planner.skipCapability(entity, "fan_on_evidence_ambiguous")
	}
	low, high, boundsOK := thermostatWriteBounds(info)
	if boundsOK {
		support.State.TemperatureMinimum, support.State.TemperatureMaximum = low, high
		if info.TwoPointTarget || info.RequiresTwoPoint {
			support.Operations.SetRange = &sdkthermostatv1.SetRangeSupport{}
		} else {
			support.Operations.SetTarget = &sdkthermostatv1.SetTargetSupport{}
		}
	} else {
		planner.skipCapability(entity, "temperature_capability_unsupported")
	}
	if info.TargetHumidity && validHumidity(info.MinimumHumidity) && validHumidity(info.MaximumHumidity) &&
		info.MinimumHumidity <= info.MaximumHumidity {
		support.State.HumidityMinimum, support.State.HumidityMaximum = info.MinimumHumidity, info.MaximumHumidity
		support.Operations.SetHumidity = &sdkthermostatv1.SetHumiditySupport{}
	} else if info.TargetHumidity {
		planner.skipCapability(entity, "humidity_capability_unsupported")
	}
	codecs, err := contractthermostatv1.Compile()
	if err != nil {
		return err
	}
	if _, err = codecs.Support.Encode(support); err != nil {
		planner.skipCapability(entity, skipCapabilityUnsupported)
		return nil //nolint:nilerr // Invalid upstream capabilities omit this Entity, not the whole Device.
	}
	if err = contractthermostatv1.ValidateSupport(support); err != nil {
		planner.skipCapability(entity, skipCapabilityUnsupported)
		return nil //nolint:nilerr // Invalid upstream capabilities omit this Entity, not the whole Device.
	}
	return planner.plan(name, identity, entityRoute{
		Upstream: upstream, Kind: routeThermostat, Thermostat: support, Climate: info,
	})
}

func thermostatFanModes(info *nativeapi.ClimateInfo) []string {
	modes := make([]string, 0, len(info.StandardFanModes)+len(info.CustomFanModes))
	for _, mode := range info.StandardFanModes {
		if mode != "on" {
			modes = append(modes, mode)
		}
	}
	for _, mode := range info.CustomFanModes {
		if nativeapi.IsStandardClimateFanMode(mode) {
			return []string{}
		}
		modes = append(modes, mode)
	}
	if len(modes) > maximumPlannedEntities {
		return []string{}
	}
	seen := make(map[string]struct{}, len(modes))
	for _, mode := range modes {
		if mode == "" || utf8.RuneCountInString(mode) > maximumNameRunes {
			return []string{}
		}
		if _, duplicate := seen[mode]; duplicate {
			return []string{}
		}
		seen[mode] = struct{}{}
	}
	return modes
}

func thermostatCelsiusValue(value float64, unit string) (float64, bool) {
	if !finiteNumber(value) {
		return 0, false
	}
	switch unit {
	case thermostatCelsius:
	case thermostatFahrenheit:
		value = (value - fahrenheitOffset) / fahrenheitScale
	case thermostatKelvin:
		value -= kelvinOffset
	default:
		return 0, false
	}
	return value, true
}

func thermostatWriteBounds(info *nativeapi.ClimateInfo) (int64, int64, bool) {
	low, lowOK := thermostatExactMilli(info.MinimumTemperature, info.TemperatureUnit)
	high, highOK := thermostatExactMilli(info.MaximumTemperature, info.TemperatureUnit)
	if !lowOK || !highOK {
		return 0, 0, false
	}
	// Integer Div floors with a positive denominator; ceil(x) is -floor(-x).
	minimum := new(big.Int).Neg(new(big.Int).Div(new(big.Int).Neg(low.Num()), low.Denom()))
	maximum := new(big.Int).Div(high.Num(), high.Denom())
	if !minimum.IsInt64() || !maximum.IsInt64() {
		return 0, 0, false
	}
	minimumMilli, maximumMilli := minimum.Int64(), maximum.Int64()
	if minimumMilli < minimumMilliTemperature || maximumMilli > maximumMilliTemperature ||
		minimumMilli > maximumMilli {
		return 0, 0, false
	}
	return minimumMilli, maximumMilli, true
}

// thermostatExactMilli preserves the decoder's decimal meaning when deriving inward write bounds.
// Binary arithmetic alone turns an exact 32.9°F maximum into 499 rather than 500 mCel.
func thermostatExactMilli(value float64, unit string) (*big.Rat, bool) {
	if !finiteNumber(value) {
		return nil, false
	}
	number, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
	if !ok {
		return nil, false
	}
	switch unit {
	case thermostatCelsius:
	case thermostatFahrenheit:
		number.Sub(number, big.NewRat(fahrenheitOffset, 1))
		number.Mul(number, big.NewRat(fahrenheitRatioDenominator, fahrenheitRatioNumerator))
	case thermostatKelvin:
		number.Sub(number, big.NewRat(kelvinOffsetHundredths, hundredthsPerDegree))
	default:
		return nil, false
	}
	return number.Mul(number, big.NewRat(milliPerDegree, 1)), true
}

func thermostatMilli(value float64, unit string) (int64, bool) {
	celsius, ok := thermostatCelsiusValue(value, unit)
	if !ok {
		return 0, false
	}
	milli := math.Round(celsius * milliPerDegree)
	if milli < minimumMilliTemperature || milli > maximumMilliTemperature {
		return 0, false
	}
	return int64(milli), true
}

func thermostatNative(milli int64, unit string) float64 {
	celsius := float64(milli) / milliPerDegree
	switch unit {
	case thermostatFahrenheit:
		return celsius*fahrenheitScale + fahrenheitOffset
	case thermostatKelvin:
		return celsius + kelvinOffset
	default:
		return celsius
	}
}

func validHumidity(value float64) bool {
	return finiteNumber(value) && value >= 0 && value <= maximumHumidity
}

func thermostatTemperature(value *float64, unit string) *int64 {
	if value == nil {
		return nil
	}
	milli, ok := thermostatMilli(*value, unit)
	if !ok {
		return nil
	}
	return &milli
}

func thermostatHumidity(value *float64) *float64 {
	if value == nil || !validHumidity(*value) {
		return nil
	}
	return value
}

func thermostatObservation(
	route entityRoute, entityID string, update nativeapi.StateUpdate, receivedAt time.Time,
) (adapter.Observation, error) {
	if update.Climate == nil || route.Climate == nil {
		return adapter.Observation{}, errUnprojectable
	}
	info, native := route.Climate, update.Climate
	state := sdkthermostatv1.State{Mode: native.Mode}
	if info.CurrentTemperature {
		state.CurrentTemperature = thermostatTemperature(native.CurrentTemperature, info.TemperatureUnit)
	}
	if info.TwoPointTarget || info.RequiresTwoPoint {
		state.TargetTemperatureLow = thermostatTemperature(native.TargetTemperatureLow, info.TemperatureUnit)
		state.TargetTemperatureHigh = thermostatTemperature(native.TargetTemperatureHigh, info.TemperatureUnit)
	} else {
		state.TargetTemperature = thermostatTemperature(native.TargetTemperature, info.TemperatureUnit)
	}
	if info.CurrentHumidity {
		state.CurrentHumidity = thermostatHumidity(native.CurrentHumidity)
	}
	if info.TargetHumidity {
		state.TargetHumidity = thermostatHumidity(native.TargetHumidity)
	}
	if info.Activity {
		state.Activity = native.Activity
	}
	if native.CustomFanMode != nil {
		if slices.Contains(info.CustomFanModes, *native.CustomFanMode) &&
			slices.Contains(route.Thermostat.State.FanModes, *native.CustomFanMode) {
			state.FanMode = native.CustomFanMode
		}
	} else if native.StandardFanMode != nil && slices.Contains(info.StandardFanModes, *native.StandardFanMode) &&
		slices.Contains(route.Thermostat.State.FanModes, *native.StandardFanMode) {
		state.FanMode = native.StandardFanMode
	}
	return sdkthermostatv1.NewObservation(sdkthermostatv1.ObservationInput{
		EntityID: entityID, Support: route.Thermostat, State: state, AdapterReceivedAt: receivedAt,
	})
}

// thermostatParameters checks operation presence before decoding and validating its required parameters.
func thermostatParameters[P, O any](
	codec *entitytypes.JSONCodec[P], raw json.RawMessage, support sdkthermostatv1.Support,
	operation *O, validate func(sdkthermostatv1.Support, O, P) error,
) (P, json.RawMessage, error) {
	var zero P
	if operation == nil {
		return zero, nil, errors.New("thermostat operation is not supported")
	}
	parameters, normalized, err := codec.Decode(raw)
	if err != nil {
		return zero, nil, err
	}
	if err = validate(support, *operation, parameters); err != nil {
		return zero, nil, err
	}
	return parameters, normalized, nil
}

func prepareThermostatCommand(
	route boundRoute, operation string, parameters json.RawMessage,
) (json.RawMessage, nativeapi.Command, error) {
	if route.Climate == nil {
		return nil, nil, errors.New("thermostat native capabilities are missing")
	}
	codecs, err := contractthermostatv1.Compile()
	if err != nil {
		return nil, nil, err
	}
	support := route.Thermostat
	native := nativeapi.ClimateCommand{Key: route.Upstream.Key, DeviceID: route.Upstream.DeviceID}
	switch operation {
	case contractthermostatv1.OperationSetMode:
		decoded, normalized, decodeErr := thermostatParameters(
			codecs.SetModeParameters, parameters, support, support.Operations.SetMode,
			contractthermostatv1.ValidateSetModeParameters,
		)
		native.Mode = &decoded.Mode
		return normalized, native, decodeErr
	case contractthermostatv1.OperationSetTarget:
		decoded, normalized, decodeErr := thermostatParameters(
			codecs.SetTargetParameters, parameters, support, support.Operations.SetTarget,
			contractthermostatv1.ValidateSetTargetParameters,
		)
		native.TargetTemperature = new(thermostatNative(decoded.Temperature, route.Climate.TemperatureUnit))
		return normalized, native, decodeErr
	case contractthermostatv1.OperationSetRange:
		decoded, normalized, decodeErr := thermostatParameters(
			codecs.SetRangeParameters, parameters, support, support.Operations.SetRange,
			contractthermostatv1.ValidateSetRangeParameters,
		)
		native.TargetTemperatureLow = new(thermostatNative(decoded.Low, route.Climate.TemperatureUnit))
		native.TargetTemperatureHigh = new(thermostatNative(decoded.High, route.Climate.TemperatureUnit))
		return normalized, native, decodeErr
	case contractthermostatv1.OperationSetFanMode:
		decoded, normalized, decodeErr := thermostatParameters(
			codecs.SetFanModeParameters, parameters, support, support.Operations.SetFanMode,
			contractthermostatv1.ValidateSetFanModeParameters,
		)
		if slices.Contains(route.Climate.CustomFanModes, decoded.FanMode) {
			native.CustomFanMode = &decoded.FanMode
		} else {
			native.StandardFanMode = &decoded.FanMode
		}
		return normalized, native, decodeErr
	case contractthermostatv1.OperationSetHumidity:
		decoded, normalized, decodeErr := thermostatParameters(
			codecs.SetHumidityParameters, parameters, support, support.Operations.SetHumidity,
			contractthermostatv1.ValidateSetHumidityParameters,
		)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		if _, err = wireNumber(decoded.Humidity); err != nil {
			return nil, nil, err
		}
		native.TargetHumidity = &decoded.Humidity
		return normalized, native, nil
	default:
		return nil, nil, errors.New("unknown thermostat operation")
	}
}

func thermostatSatisfied(operation string, parameters, value json.RawMessage) bool {
	var state contractthermostatv1.State
	if json.Unmarshal(value, &state) != nil {
		return false
	}
	switch operation {
	case contractthermostatv1.OperationSetMode:
		var target contractthermostatv1.SetModeParameters
		return json.Unmarshal(parameters, &target) == nil && contractthermostatv1.SetModeSatisfied(target, state)
	case contractthermostatv1.OperationSetTarget:
		var target contractthermostatv1.SetTargetParameters
		return json.Unmarshal(parameters, &target) == nil && contractthermostatv1.SetTargetSatisfied(target, state)
	case contractthermostatv1.OperationSetRange:
		var target contractthermostatv1.SetRangeParameters
		return json.Unmarshal(parameters, &target) == nil && contractthermostatv1.SetRangeSatisfied(target, state)
	case contractthermostatv1.OperationSetFanMode:
		var target contractthermostatv1.SetFanModeParameters
		return json.Unmarshal(parameters, &target) == nil && contractthermostatv1.SetFanModeSatisfied(target, state)
	case contractthermostatv1.OperationSetHumidity:
		var target contractthermostatv1.SetHumidityParameters
		return json.Unmarshal(parameters, &target) == nil && contractthermostatv1.SetHumiditySatisfied(target, state)
	default:
		return false
	}
}
