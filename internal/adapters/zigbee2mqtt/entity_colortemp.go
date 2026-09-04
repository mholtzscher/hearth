package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
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

// newColorTempPlan builds the complete color-temperature translation for one State property.
func newColorTempPlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum int64,
) (entityPlan, error) {
	support := colorTempSupport(minimum, maximum)
	descriptor, descriptorErr := sdkcolortempv1.NewEntityDescriptor(metadata, support)
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
			value, err := normalizeColorTemp(raw, minimum, maximum)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdkcolortempv1.NewObservation(sdkcolortempv1.ObservationInput{
				EntityID: entityID, Support: support, State: contractcolortempv1.State(value),
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
			var value int64
			var deadline time.Time
			handler, err := sdkcolortempv1.NewCommandHandler(entityID, support, sdkcolortempv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractcolortempv1.SetParameters],
					_ adapter.Responder,
				) error {
					value = typedCommand.Parameters.Value
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
			translated, valueErr := colorTempCommandValue(value)
			if valueErr != nil {
				return plannedCommand{}, valueErr
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: translated},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches:       exactMatcher(value),
			}, nil
		},
	}, nil
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
	if feature.Access&requiredAccessMask != requiredAccessMask || feature.Property == "" {
		return 0, 0, false
	}
	minimum, minimumOK := colorTempBound(feature.valueMinRaw, feature.ValueMin)
	maximum, maximumOK := colorTempBound(feature.valueMaxRaw, feature.ValueMax)
	if !minimumOK || !maximumOK || minimum < hearthColorTempMinimum || maximum > hearthColorTempMaximum ||
		minimum >= maximum {
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
	var decoded any
	if decodeJSON(payload, &decoded) != nil {
		return 0, false
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, false
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, false
	}
	return exact.Num().Int64(), true
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

func colorTempCommandValue(value int64) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode color temperature: %w", err)
	}
	return encoded, nil
}
