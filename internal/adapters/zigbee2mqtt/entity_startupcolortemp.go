package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"time"

	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	startupColorTempExposeName = "color_temp_startup"
	// startupPreviousWireValue is the adapter-local Zigbee2MQTT sentinel for
	// the named previous choice. It never appears as public Hearth state.
	startupPreviousWireValue = 65535
	// upstreamPreviousPreset is the preset name advertising the sentinel.
	upstreamPreviousPreset = "previous"
	startupTempUnit        = "mired"
)

// newStartupColorTempPlan builds the complete startup-temperature setting
// translation for one State property. Numeric values travel as exact
// integers; the named previous choice travels as the 65535 sentinel only
// when the expose advertises that mapping.
func newStartupColorTempPlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum int64,
	choices []string,
) (entityPlan, error) {
	if choices == nil {
		choices = []string{}
	}
	support := startupColorTempSupport(minimum, maximum, choices)
	descriptor, descriptorErr := sdknumericsettingv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	hasPrevious := startupHasPrevious(choices)
	return entityPlan{
		Descriptor:      descriptor,
		StateProperties: []string{property},
		GetProperties:   []string{property},
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			receivedAt time.Time,
		) (stateReport, bool, error) {
			raw, present := properties[property]
			if !present {
				return stateReport{}, false, nil
			}
			state, err := decodeStartupColorTempState(raw, minimum, maximum, hasPrevious)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdknumericsettingv1.NewObservation(sdknumericsettingv1.ObservationInput{
				EntityID: entityID, Support: support, State: state,
				AdapterReceivedAt: receivedAt,
			})
			if err != nil {
				return stateReport{}, false, err
			}
			return stateReport{Observation: observation, semantic: state}, true, nil
		},
		TranslateCommand: func(
			ctx context.Context,
			entityID string,
			command adapter.Command,
			responder adapter.Responder,
		) (plannedCommand, error) {
			var parameters contractnumericsettingv1.SetParameters
			var deadline time.Time
			handler, err := sdknumericsettingv1.NewCommandHandler(entityID, support, sdknumericsettingv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractnumericsettingv1.SetParameters],
					_ adapter.Responder,
				) error {
					parameters = typedCommand.Parameters
					deadline = typedCommand.Deadline
					return nil
				},
			})
			if err != nil {
				return plannedCommand{}, err
			}
			if err = handler(ctx, command, responder); err != nil {
				return plannedCommand{}, err
			}
			wire, err := startupColorTempCommandValue(parameters, minimum, maximum, hasPrevious)
			if err != nil {
				return plannedCommand{}, err
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: wire},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches: func(report stateReport) bool {
					state, ok := report.semantic.(contractnumericsettingv1.State)
					if !ok {
						return false
					}
					return contractnumericsettingv1.SetSatisfied(parameters, state)
				},
			}, nil
		},
	}, nil
}

func startupColorTempSupport(
	minimum, maximum int64,
	choices []string,
) contractnumericsettingv1.Support {
	unit := startupTempUnit
	return contractnumericsettingv1.Support{
		State: contractnumericsettingv1.StateSupport{
			Minimum: float64(minimum),
			Maximum: float64(maximum),
			Unit:    &unit,
			Choices: choices,
		},
		Operations: contractnumericsettingv1.OperationSupport{Set: contractnumericsettingv1.SetSupport{}},
	}
}

func startupHasPrevious(choices []string) bool {
	return slices.Contains(choices, upstreamPreviousPreset)
}

// decodeStartupColorTempState maps one wire reading to setting state: the
// 65535 sentinel becomes the previous choice only when advertised, and any
// other value must be an exact integer inside the discovered bounds.
func decodeStartupColorTempState(
	payload json.RawMessage,
	minimum, maximum int64,
	hasPrevious bool,
) (contractnumericsettingv1.State, error) {
	value, err := normalizeStartupColorTemp(payload, minimum, maximum, hasPrevious)
	if err != nil {
		return contractnumericsettingv1.State{}, err
	}
	if value == startupPreviousWireValue {
		choice := upstreamPreviousPreset
		return contractnumericsettingv1.State{Mode: "choice", Choice: &choice}, nil
	}
	mireds := float64(value)
	return contractnumericsettingv1.State{Mode: "value", Value: &mireds}, nil
}

func normalizeStartupColorTemp(
	payload json.RawMessage,
	minimum, maximum int64,
	hasPrevious bool,
) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, fmt.Errorf("decode startup color temperature number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errors.New("startup color temperature value must be a JSON number")
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, errors.New("startup color temperature value must be a finite integer")
	}
	value := exact.Num().Int64()
	if value == startupPreviousWireValue {
		if !hasPrevious {
			return 0, errors.New("startup color temperature previous choice is not supported")
		}
		return value, nil
	}
	if value < minimum || value > maximum {
		return 0, errors.New("startup color temperature value is outside its discovered range")
	}
	return value, nil
}

// startupColorTempCommandValue maps typed setting parameters to the wire
// value, enforcing this bulb's integer-only wire contract before any MQTT
// publish: fractional in-range values pass generic validation but are
// rejected here.
func startupColorTempCommandValue(
	parameters contractnumericsettingv1.SetParameters,
	minimum, maximum int64,
	hasPrevious bool,
) (json.RawMessage, error) {
	switch parameters.Mode {
	case "choice":
		if parameters.Choice == nil || *parameters.Choice != upstreamPreviousPreset || !hasPrevious {
			return nil, errors.New("startup color temperature choice is not supported")
		}
		encoded, err := json.Marshal(startupPreviousWireValue)
		if err != nil {
			return nil, fmt.Errorf("encode startup color temperature: %w", err)
		}
		return encoded, nil
	case "value":
		if parameters.Value == nil {
			return nil, errors.New("startup color temperature value is required")
		}
		value := *parameters.Value
		if !isFinite(value) || math.Trunc(value) != value {
			return nil, errors.New("startup color temperature value must be an integer")
		}
		if value < float64(minimum) || value > float64(maximum) {
			return nil, errors.New("startup color temperature value is outside its discovered range")
		}
		encoded, err := json.Marshal(int64(value))
		if err != nil {
			return nil, fmt.Errorf("encode startup color temperature: %w", err)
		}
		return encoded, nil
	default:
		return nil, errors.New("startup color temperature mode must be value or choice")
	}
}

// startupTempRange validates the exact-integer mired bounds of a
// color_temp_startup feature: both bounds exact integers within 100–1000
// with minimum < maximum.
func startupTempRange(feature upstreamExpose) (int64, int64, bool) {
	if feature.Property == "" {
		return 0, 0, false
	}
	// The exact-integer mired bound semantics are shared with color_temp;
	// only the property and preset handling are startup-specific.
	minimum, minimumOK := colorTempBound(feature.valueMinRaw, feature.ValueMin)
	maximum, maximumOK := colorTempBound(feature.valueMaxRaw, feature.ValueMax)
	if !minimumOK || !maximumOK || minimum < hearthColorTempMinimum || maximum > hearthColorTempMaximum ||
		minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

// startupPreviousChoices resolves the advertised named choices for a
// color_temp_startup feature. No previous preset yields an empty choice
// set; exactly one previous↔65535 preset yields ["previous"]; an advertised
// but invalid or duplicate previous mapping makes the feature ineligible.
// Other numeric presets are wire aliases and never enter choices.
func startupPreviousChoices(feature upstreamExpose) ([]string, bool) {
	if feature.previousInvalid {
		return nil, false
	}
	previous := 0
	for _, preset := range feature.Presets {
		if preset.Name != upstreamPreviousPreset {
			continue
		}
		if preset.Value != startupPreviousWireValue {
			return nil, false
		}
		previous++
	}
	if previous > 1 {
		return nil, false
	}
	if previous == 1 {
		return []string{upstreamPreviousPreset}, true
	}
	return []string{}, true
}
