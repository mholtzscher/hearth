package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractcolorxyv1 "github.com/mholtzscher/hearth/entitytypes/colorxyv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkcolorxyv1 "github.com/mholtzscher/hearth/sdk/adapter/colorxyv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

// newColorXYPlan builds the native XY chromaticity translation for one color
// property and its companion mode property. XY is active exactly when the
// same-message mode is xy; complete but inactive values publish with
// active:false.
//
//nolint:dupl // XY and HS are parallel native representations over distinct generated contracts.
func newColorXYPlan(
	metadata adapter.EntityMetadata,
	colorProperty, modeProperty string,
) (entityPlan, error) {
	support := sdkcolorxyv1.Support{}
	descriptor, descriptorErr := sdkcolorxyv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	return entityPlan{
		Descriptor:      descriptor,
		StateProperties: []string{colorProperty, modeProperty},
		GetProperties:   []string{colorProperty},
		DecodeState: func(
			entityID string,
			properties map[string]json.RawMessage,
			receivedAt time.Time,
		) (stateReport, bool, error) {
			state, decoded, err := decodeColorXYState(properties, colorProperty, modeProperty)
			if err != nil {
				return stateReport{}, false, err
			}
			if !decoded {
				return stateReport{}, false, nil
			}
			observation, err := sdkcolorxyv1.NewObservation(sdkcolorxyv1.ObservationInput{
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
			var parameters contractcolorxyv1.SetParameters
			var deadline time.Time
			handler, err := sdkcolorxyv1.NewCommandHandler(entityID, support, sdkcolorxyv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractcolorxyv1.SetParameters],
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
			payload, err := colorXYCommandValue(parameters.X, parameters.Y)
			if err != nil {
				return plannedCommand{}, err
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{colorProperty: payload},
				GetProperties: []string{colorProperty},
				Deadline:      deadline,
				Matches: func(report stateReport) bool {
					state, ok := report.semantic.(contractcolorxyv1.State)
					if !ok {
						return false
					}
					return contractcolorxyv1.SetSatisfied(parameters, state)
				},
			}, nil
		},
	}, nil
}

func decodeColorXYState(
	properties map[string]json.RawMessage,
	colorProperty, modeProperty string,
) (contractcolorxyv1.State, bool, error) {
	rawMode, present := properties[modeProperty]
	if !present {
		return contractcolorxyv1.State{}, false, nil
	}
	mode, err := decodeReportedColorMode(rawMode)
	if err != nil {
		return contractcolorxyv1.State{}, false, fmt.Errorf("color XY value: %w", err)
	}
	rawColor, present := properties[colorProperty]
	if !present {
		return contractcolorxyv1.State{}, false, nil
	}
	fields, err := colorObjectFields(rawColor)
	if err != nil {
		return contractcolorxyv1.State{}, false, fmt.Errorf("color XY value: %w", err)
	}
	rawX, ok := fields["x"]
	if !ok {
		return contractcolorxyv1.State{}, false, errors.New("color XY value is missing its x coordinate")
	}
	rawY, ok := fields["y"]
	if !ok {
		return contractcolorxyv1.State{}, false, errors.New("color XY value is missing its y coordinate")
	}
	x, err := decodeScaledCoordinate(rawX, maxRawXY, colorXYScale)
	if err != nil {
		return contractcolorxyv1.State{}, false, fmt.Errorf("color XY value: %w", err)
	}
	y, err := decodeScaledCoordinate(rawY, maxRawXY, colorXYScale)
	if err != nil {
		return contractcolorxyv1.State{}, false, fmt.Errorf("color XY value: %w", err)
	}
	return contractcolorxyv1.State{Active: mode == colorModeXY, X: x, Y: y}, true, nil
}

// colorXYCommandValue encodes scaled XY integers as exact base-10 decimal
// JSON without float arithmetic.
func colorXYCommandValue(x, y int64) (json.RawMessage, error) {
	if x < 0 || x > colorXYScale || y < 0 || y > colorXYScale {
		return nil, errors.New("color XY coordinates are outside Hearth's range")
	}
	return json.RawMessage(
		`{"x":` + formatScaledUnit(x) + `,"y":` + formatScaledUnit(y) + `}`,
	), nil
}
