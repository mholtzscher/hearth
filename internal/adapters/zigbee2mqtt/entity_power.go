package zigbee2mqtt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

// newPowerPlan builds the complete power translation for one State property.
// The same constructor serves light and relay roots so State and Command
// behavior stay identical across primary families.
func newPowerPlan(
	metadata adapter.EntityMetadata,
	property string,
	on, off scalarValue,
) (entityPlan, error) {
	descriptor, descriptorErr := sdkpowerv1.NewEntityDescriptor(metadata, powerSupport())
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
			value, err := decodePowerState(raw, on, off)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
				EntityID: entityID, Support: powerSupport(), State: contractpowerv1.State(value),
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
			var setTo bool
			var deadline time.Time
			handler, err := sdkpowerv1.NewCommandHandler(entityID, powerSupport(), sdkpowerv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractpowerv1.SetParameters],
					_ adapter.Responder,
				) error {
					setTo = typedCommand.Parameters.Value
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
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: powerCommandValue(on, off, setTo)},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches:       exactMatcher(setTo),
			}, nil
		},
	}, nil
}

func powerSupport() contractpowerv1.Support {
	return contractpowerv1.Support{
		State:      contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}},
	}
}

func validPowerFeature(feature upstreamExpose) bool {
	return feature.Access&requiredAccessMask == requiredAccessMask && feature.Property != "" &&
		len(feature.ValueOn) != 0 && len(feature.ValueOff) != 0
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
		return false, fmt.Errorf("power scalar does not match value_on or value_off")
	}
}

// powerCommandValue returns the exact discovered scalar for a typed power command.
func powerCommandValue(on, off scalarValue, value bool) json.RawMessage {
	if value {
		return bytes.Clone(on.Raw)
	}
	return bytes.Clone(off.Raw)
}
