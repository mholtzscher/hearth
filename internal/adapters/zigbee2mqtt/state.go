package zigbee2mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
)

const brightnessRoundingOffset = 0.5

// decodedEntityState is one typed Hearth value joined to the discovery route that produced it.
type decodedEntityState struct {
	Entity     discoveredEntity
	Power      bool
	Brightness int64
	ColorTemp  int64
}

// stateDecodeIssue identifies one recognized property rejected without suppressing valid siblings.
type stateDecodeIssue struct {
	Property string
	Err      error
}

// decodeDeviceState projects recognized properties in discovery order and isolates invalid values.
func decodeDeviceState(
	payload []byte,
	entities []discoveredEntity,
) ([]decodedEntityState, []stateDecodeIssue, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil, errors.New("device State must be a JSON object")
	}
	var properties map[string]json.RawMessage
	if err := decodeJSON(payload, &properties); err != nil {
		return nil, nil, fmt.Errorf("decode Zigbee2MQTT device State: %w", err)
	}
	states := make([]decodedEntityState, 0, len(entities))
	issues := make([]stateDecodeIssue, 0)
	for _, entity := range entities {
		raw, present := properties[entity.Property]
		if !present {
			continue
		}
		state, err := decodeEntityState(entity, raw)
		if err != nil {
			issues = append(issues, stateDecodeIssue{Property: entity.Property, Err: err})
			continue
		}
		states = append(states, state)
	}
	return states, issues, nil
}

func decodeEntityState(entity discoveredEntity, payload json.RawMessage) (decodedEntityState, error) {
	switch entity.Kind {
	case entityKindPower:
		value, err := decodePowerState(payload, entity.PowerOn, entity.PowerOff)
		if err != nil {
			return decodedEntityState{}, err
		}
		return decodedEntityState{Entity: entity, Power: value}, nil
	case entityKindBrightness:
		value, err := normalizeBrightness(payload, entity.BrightnessMaximum)
		if err != nil {
			return decodedEntityState{}, err
		}
		return decodedEntityState{Entity: entity, Brightness: value}, nil
	case entityKindColorTemp:
		value, err := normalizeColorTemp(payload, entity.ColorTempMinimum, entity.ColorTempMaximum)
		if err != nil {
			return decodedEntityState{}, err
		}
		return decodedEntityState{Entity: entity, ColorTemp: value}, nil
	default:
		return decodedEntityState{}, errors.New("unknown discovered Entity kind")
	}
}

func decodePowerState(payload json.RawMessage, on, off scalarValue) (bool, error) {
	value, err := canonicalScalar(payload)
	if err != nil {
		return false, fmt.Errorf("decode power scalar: %w", err)
	}
	switch value.canonical {
	case on.canonical:
		return true, nil
	case off.canonical:
		return false, nil
	default:
		return false, errors.New("power scalar does not match value_on or value_off")
	}
}

// normalizeBrightness decodes a JSON number with UseNumber and rounds it to Hearth's 0..100 State.
func normalizeBrightness(payload json.RawMessage, maximum float64) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode brightness number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("brightness value must be a JSON number")
	}
	value, err := number.Float64()
	if err != nil {
		return 0, fmt.Errorf("convert brightness number: %w", err)
	}
	return normalizeBrightnessValue(value, maximum)
}

func normalizeColorTemp(payload json.RawMessage, minimum, maximum int64) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode color temperature number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("color temperature value must be a JSON number")
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, errors.New("color temperature value must be a finite integer")
	}
	value := exact.Num().Int64()
	if value < minimum || value > maximum {
		return 0, errors.New("color temperature value is outside its discovered range")
	}
	return value, nil
}

func normalizeBrightnessValue(value, maximum float64) (int64, error) {
	if !isFinite(value) || !isFinite(maximum) || maximum <= 0 {
		return 0, errors.New("brightness value and maximum must be finite with a positive maximum")
	}
	if value < 0 || value > maximum {
		return 0, errors.New("brightness value is outside its discovered range")
	}
	normalized := math.Floor(value*hearthBrightnessMaximum/maximum + brightnessRoundingOffset)
	if normalized < 0 || normalized > hearthBrightnessMaximum {
		return 0, errors.New("normalized brightness is outside Hearth's range")
	}
	return int64(normalized), nil
}

// powerCommandValue returns the exact discovered scalar for a typed power command.
func powerCommandValue(entity discoveredEntity, value bool) (json.RawMessage, error) {
	if entity.Kind != entityKindPower {
		return nil, errors.New("power command requires a power Entity")
	}
	if value {
		return bytes.Clone(entity.PowerOn.Raw), nil
	}
	return bytes.Clone(entity.PowerOff.Raw), nil
}

// brightnessCommandValue scales a typed Hearth percentage to the discovered upstream maximum.
func brightnessCommandValue(entity discoveredEntity, percentage int64) (json.RawMessage, error) {
	if entity.Kind != entityKindBrightness {
		return nil, errors.New("brightness command requires a brightness Entity")
	}
	return scaleBrightnessCommand(percentage, entity.BrightnessMaximum)
}

func colorTempCommandValue(entity discoveredEntity, value int64) (json.RawMessage, error) {
	if entity.Kind != entityKindColorTemp {
		return nil, errors.New("color temperature command requires a color-temperature Entity")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode color temperature: %w", err)
	}
	return encoded, nil
}

func scaleBrightnessCommand(percentage int64, maximum float64) (json.RawMessage, error) {
	if percentage < 0 || percentage > hearthBrightnessMaximum {
		return nil, errors.New("brightness percentage is outside Hearth's range")
	}
	if !isFinite(maximum) || maximum <= 0 {
		return nil, errors.New("brightness maximum must be finite and positive")
	}
	scaled := float64(percentage) * maximum / hearthBrightnessMaximum
	if !isFinite(scaled) {
		return nil, errors.New("scaled brightness is not finite")
	}
	encoded, err := json.Marshal(scaled)
	if err != nil {
		return nil, fmt.Errorf("encode scaled brightness: %w", err)
	}
	return encoded, nil
}
