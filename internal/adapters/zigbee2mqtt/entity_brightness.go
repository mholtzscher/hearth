package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	hearthBrightnessMaximum  = 100
	brightnessRoundingOffset = 0.5
)

// newBrightnessPlan builds the complete brightness translation for one State property.
func newBrightnessPlan(
	metadata adapter.EntityMetadata,
	property string,
	maximum float64,
) (entityPlan, error) {
	descriptor, descriptorErr := sdkbrightnessv1.NewEntityDescriptor(metadata, brightnessSupport())
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
			value, err := normalizeBrightness(raw, maximum)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
				EntityID: entityID, Support: brightnessSupport(), State: contractbrightnessv1.State(value),
				AdapterReceivedAt: receivedAt,
			})
			if err != nil {
				return stateReport{}, false, err
			}
			return stateReport{Observation: observation, semantic: value}, true, nil
		},
		TranslateCommand: func(
			ctx context.Context,
			entityID string,
			command adapter.Command,
			responder adapter.Responder,
		) (plannedCommand, error) {
			var percentage int64
			var deadline time.Time
			handler, err := sdkbrightnessv1.NewCommandHandler(
				entityID,
				brightnessSupport(),
				sdkbrightnessv1.Handlers{
					Set: func(
						_ context.Context,
						typedCommand typed.Command[contractbrightnessv1.SetParameters],
						_ adapter.Responder,
					) error {
						percentage = typedCommand.Parameters.Value
						deadline = typedCommand.Deadline
						return nil
					},
				},
			)
			if err != nil {
				return plannedCommand{}, err
			}
			if err = handler(ctx, command, responder); err != nil {
				return plannedCommand{}, err
			}
			scaled, err := brightnessCommandValue(maximum, percentage)
			if err != nil {
				return plannedCommand{}, err
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: scaled},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches:       exactMatcher(percentage),
			}, nil
		},
	}, nil
}

func brightnessSupport() contractbrightnessv1.Support {
	return contractbrightnessv1.Support{
		State:      contractbrightnessv1.StateSupport{Maximum: hearthBrightnessMaximum},
		Operations: contractbrightnessv1.OperationSupport{Set: contractbrightnessv1.SetSupport{Step: 1}},
	}
}

func validBrightnessFeature(feature upstreamExpose) bool {
	return feature.Access&requiredAccessMask == requiredAccessMask && feature.Property != "" &&
		feature.ValueMin != nil && *feature.ValueMin == 0 &&
		feature.ValueMax != nil && brightnessRangeSupported(*feature.ValueMax)
}

func brightnessRangeSupported(maximum float64) bool {
	if !isFinite(maximum) || maximum < hearthBrightnessMaximum {
		return false
	}
	for percentage := int64(0); percentage <= hearthBrightnessMaximum; percentage++ {
		scaled := float64(percentage) * maximum / hearthBrightnessMaximum
		normalized, err := normalizeBrightnessValue(scaled, maximum)
		if err != nil || normalized != percentage {
			return false
		}
	}
	return true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
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

// brightnessCommandValue scales a typed Hearth percentage to the discovered upstream maximum.
func brightnessCommandValue(maximum float64, percentage int64) (json.RawMessage, error) {
	return scaleBrightnessCommand(percentage, maximum)
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
