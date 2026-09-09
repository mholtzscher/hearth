package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
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
			state, err := decodeStartupColorTempState(raw)
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
			wire, err := startupColorTempCommandValue(parameters)
			if err != nil {
				// Adapter-local validation runs after generic support
				// validation, so a fractional in-range value reaches
				// here with no responder consumption yet. Reject
				// promptly with the ordinary rejection instead of
				// leaving the command without a response, and never
				// classify it as unavailable. A failed rejection
				// publication surfaces as the handler error.
				if rejectErr := responder.Reject(err.Error()); rejectErr != nil {
					return plannedCommand{}, rejectErr
				}
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

// decodeStartupColorTempState maps the 65535 sentinel to the previous choice
// and other exact integers to mired values. NewObservation validates both
// the discovered bounds and whether the previous choice is supported.
func decodeStartupColorTempState(payload json.RawMessage) (contractnumericsettingv1.State, error) {
	value, err := normalizeStartupColorTemp(payload)
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

// normalizeStartupColorTemp enforces the integer-only Zigbee wire format;
// bounds and choice membership belong to the typed observation.
func normalizeStartupColorTemp(payload json.RawMessage) (int64, error) {
	value, err := parseExactIntegerJSON(payload)
	if err != nil {
		if errors.Is(err, errExactIntegerNotNumber) {
			return 0, errors.New("startup color temperature value must be a JSON number")
		}
		if errors.Is(err, errExactIntegerNotInteger) {
			return 0, errors.New("startup color temperature value must be a finite integer")
		}
		return 0, fmt.Errorf("decode startup color temperature number: %w", err)
	}
	return value, nil
}

// startupColorTempCommandValue encodes parameters validated by the command
// handler. Discovery admits only the previous choice; numeric values still
// need the integer-only Zigbee check because numericsetting allows fractions.
func startupColorTempCommandValue(parameters contractnumericsettingv1.SetParameters) (json.RawMessage, error) {
	if parameters.Mode == "choice" {
		return json.Marshal(startupPreviousWireValue)
	}
	value := *parameters.Value
	if math.Trunc(value) != value {
		return nil, errors.New("startup color temperature value must be an integer")
	}
	return json.Marshal(int64(value))
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
