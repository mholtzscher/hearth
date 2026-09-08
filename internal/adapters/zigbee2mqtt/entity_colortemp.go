package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkcolortempv1 "github.com/mholtzscher/hearth/sdk/adapter/colortempv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	hearthColorTempMinimum = 100
	hearthColorTempMaximum = 1000
)

// newColorTempPlan builds the complete color-temperature translation for one
// State property. When mode is required the plan also claims the companion
// mode property and temperature is active exactly when the same-message mode
// is color_temp. Without a required mode the decoder still inspects a
// reported mode when one is present, and an empty mode property disables
// mode inspection entirely.
func newColorTempPlan(
	metadata adapter.EntityMetadata,
	property string,
	modeProperty string,
	requireMode bool,
	minimum, maximum int64,
) (entityPlan, error) {
	support := colorTempSupport(minimum, maximum)
	descriptor, descriptorErr := sdkcolortempv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	stateProperties := []string{property}
	if requireMode {
		stateProperties = []string{property, modeProperty}
	}
	return entityPlan{
		Descriptor:      descriptor,
		StateProperties: stateProperties,
		GetProperties:   []string{property},
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			receivedAt time.Time,
		) (stateReport, bool, error) {
			state, decoded, err := decodeColorTempState(properties, property, modeProperty, requireMode)
			if err != nil {
				return stateReport{}, false, err
			}
			if !decoded {
				return stateReport{}, false, nil
			}
			observation, err := sdkcolortempv1.NewObservation(sdkcolortempv1.ObservationInput{
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
			var parameters contractcolortempv1.SetParameters
			var deadline time.Time
			handler, err := sdkcolortempv1.NewCommandHandler(entityID, support, sdkcolortempv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractcolortempv1.SetParameters],
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
			translated, valueErr := colorTempCommandValue(parameters.Value)
			if valueErr != nil {
				return plannedCommand{}, valueErr
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: translated},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches: func(report stateReport) bool {
					state, ok := report.semantic.(contractcolortempv1.State)
					if !ok {
						return false
					}
					return contractcolortempv1.SetSatisfied(parameters, state)
				},
			}, nil
		},
	}, nil
}

func decodeColorTempState(
	properties map[string]json.RawMessage,
	property, modeProperty string,
	requireMode bool,
) (contractcolortempv1.State, bool, error) {
	raw, present := properties[property]
	if !present {
		return contractcolortempv1.State{}, false, nil
	}
	value, err := normalizeColorTemp(raw)
	if err != nil {
		return contractcolortempv1.State{}, false, err
	}
	active, decoded, err := decodeColorTempActivity(properties, modeProperty, requireMode)
	if err != nil || !decoded {
		return contractcolortempv1.State{}, decoded, err
	}
	return contractcolortempv1.State{Active: active, Value: value}, true, nil
}

// decodeColorTempActivity derives temperature activity from the companion
// mode: active exactly when the same-message mode is color_temp, always
// active when no mode is inspected, and falling back to always-active when
// mode is absent without a requirement.
func decodeColorTempActivity(
	properties map[string]json.RawMessage,
	modeProperty string,
	requireMode bool,
) (bool, bool, error) {
	if modeProperty == "" {
		return true, true, nil
	}
	rawMode, present := properties[modeProperty]
	if !present {
		return true, !requireMode, nil
	}
	mode, err := decodeReportedColorMode(rawMode)
	if err != nil {
		return false, false, fmt.Errorf("color temperature mode: %w", err)
	}
	return mode == colorModeColorTemp, true, nil
}

func colorTempSupport(minimum, maximum int64) contractcolortempv1.Support {
	return contractcolortempv1.Support{
		State: contractcolortempv1.StateSupport{
			Minimum: minimum,
			Maximum: maximum,
		},
		Operations: contractcolortempv1.OperationSupport{Set: contractcolortempv1.SetSupport{Step: 1}},
	}
}

func colorTempRange(feature upstreamExpose) (int64, int64, bool) {
	if !exposeCanPublish(feature) || !exposeCanSet(feature) || !exposeCanGet(feature) ||
		feature.Property == "" {
		return 0, 0, false
	}
	minimum, minimumOK := colorTempBound(feature.valueMinRaw, feature.ValueMin)
	maximum, maximumOK := colorTempBound(feature.valueMaxRaw, feature.ValueMax)
	// The 100..1000 outer envelope is enforced by the colortemp descriptor
	// support codec in newColorTempPlan; discovery only orders exact bounds.
	if !minimumOK || !maximumOK || minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

func colorTempBound(payload json.RawMessage, fallback *float64) (int64, bool) {
	if len(payload) == 0 {
		if fallback == nil || !isFinite(*fallback) || math.Trunc(*fallback) != *fallback ||
			*fallback < hearthColorTempMinimum || *fallback > hearthColorTempMaximum {
			return 0, false
		}
		return int64(*fallback), true
	}
	value, err := parseExactIntegerJSON(payload)
	if err != nil {
		return 0, false
	}
	return value, true
}

func normalizeColorTemp(payload json.RawMessage) (int64, error) {
	value, err := parseExactIntegerJSON(payload)
	if err != nil {
		if errors.Is(err, errExactIntegerNotNumber) {
			return 0, errors.New("color temperature value must be a JSON number")
		}
		if errors.Is(err, errExactIntegerNotInteger) {
			return 0, errors.New("color temperature value must be a finite integer")
		}
		return 0, fmt.Errorf("decode color temperature number: %w", err)
	}
	// The discovered and outer ranges are enforced by the colortemp
	// NewObservation contract; decoding only establishes an exact integer.
	return value, nil
}

func colorTempCommandValue(value int64) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode color temperature: %w", err)
	}
	return encoded, nil
}
