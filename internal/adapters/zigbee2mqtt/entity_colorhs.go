package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractcolorhsv1 "github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkcolorhsv1 "github.com/mholtzscher/hearth/sdk/adapter/colorhsv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

// newColorHSPlan builds the native hue/saturation translation for one color
// property and its companion mode property. Both coordinates are mandatory:
// partial HS commands do not exist, and a partial pair inside a present color
// object is invalid for this representation. HS is active exactly when the
// same-message mode is hs.
//
//nolint:dupl // XY and HS are parallel native representations over distinct generated contracts.
func newColorHSPlan(
	metadata adapter.EntityMetadata,
	colorProperty, modeProperty string,
) (entityPlan, error) {
	support := sdkcolorhsv1.Support{}
	descriptor, descriptorErr := sdkcolorhsv1.NewEntityDescriptor(metadata, support)
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
			state, decoded, err := decodeColorHSState(properties, colorProperty, modeProperty)
			if err != nil {
				return stateReport{}, false, err
			}
			if !decoded {
				return stateReport{}, false, nil
			}
			observation, err := sdkcolorhsv1.NewObservation(sdkcolorhsv1.ObservationInput{
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
			var parameters contractcolorhsv1.SetParameters
			var deadline time.Time
			handler, err := sdkcolorhsv1.NewCommandHandler(entityID, support, sdkcolorhsv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractcolorhsv1.SetParameters],
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
			payload := colorHSCommandValue(parameters.Hue, parameters.Saturation)
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{colorProperty: payload},
				GetProperties: []string{colorProperty},
				Deadline:      deadline,
				Matches: func(report stateReport) bool {
					state, ok := report.semantic.(contractcolorhsv1.State)
					if !ok {
						return false
					}
					return contractcolorhsv1.SetSatisfied(parameters, state)
				},
			}, nil
		},
	}, nil
}

// hueExclusiveMaximum bounds canonical hue to 0..359; observed 360 folds to 0.
const hueExclusiveMaximum = 360

func decodeColorHSState(
	properties map[string]json.RawMessage,
	colorProperty, modeProperty string,
) (contractcolorhsv1.State, bool, error) {
	rawMode, present := properties[modeProperty]
	if !present {
		return contractcolorhsv1.State{}, false, nil
	}
	mode, err := decodeReportedColorMode(rawMode)
	if err != nil {
		return contractcolorhsv1.State{}, false, fmt.Errorf("color HS value: %w", err)
	}
	rawColor, present := properties[colorProperty]
	if !present {
		return contractcolorhsv1.State{}, false, nil
	}
	fields, err := colorObjectFields(rawColor)
	if err != nil {
		return contractcolorhsv1.State{}, false, fmt.Errorf("color HS value: %w", err)
	}
	rawHue, ok := fields["hue"]
	if !ok {
		return contractcolorhsv1.State{}, false, errors.New("color HS value is missing its hue coordinate")
	}
	rawSaturation, ok := fields["saturation"]
	if !ok {
		return contractcolorhsv1.State{}, false, errors.New("color HS value is missing its saturation coordinate")
	}
	hue, err := decodeScaledCoordinate(rawHue, maxRawHue, 1)
	if err != nil {
		return contractcolorhsv1.State{}, false, fmt.Errorf("color HS value: %w", err)
	}
	if hue >= hueExclusiveMaximum {
		hue = 0
	}
	saturation, err := decodeScaledCoordinate(rawSaturation, maxRawSaturation, 1)
	if err != nil {
		return contractcolorhsv1.State{}, false, fmt.Errorf("color HS value: %w", err)
	}
	return contractcolorhsv1.State{Active: mode == colorModeHS, Hue: hue, Saturation: saturation}, true, nil
}

// colorHSCommandValue encodes integer hue and saturation directly. The command
// schema rejects hue 360, so no folding happens here. The Hearth coordinate
// range is enforced by the color HS NewCommandHandler contract.
func colorHSCommandValue(hue, saturation int64) json.RawMessage {
	return json.RawMessage(
		fmt.Sprintf(`{"hue":%d,"saturation":%d}`, hue, saturation),
	)
}
