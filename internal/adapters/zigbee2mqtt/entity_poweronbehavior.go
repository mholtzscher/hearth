package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	contractenumsettingv1 "github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const powerOnBehaviorExposeName = "power_on_behavior"

// newPowerOnBehaviorPlan builds the complete power-on behavior setting
// translation for one State property. Choices come from the expose values
// and are never hard-coded; validation and satisfaction reuse the generated
// enumsetting behavior.
func newPowerOnBehaviorPlan(
	metadata adapter.EntityMetadata,
	property string,
	choices []string,
) (entityPlan, error) {
	support := powerOnBehaviorSupport(choices)
	descriptor, descriptorErr := sdkenumsettingv1.NewEntityDescriptor(metadata, support)
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
			state, err := decodePowerOnBehaviorState(raw, choices)
			if err != nil {
				return stateReport{}, false, err
			}
			observation, err := sdkenumsettingv1.NewObservation(sdkenumsettingv1.ObservationInput{
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
			var parameters contractenumsettingv1.SetParameters
			var deadline time.Time
			handler, err := sdkenumsettingv1.NewCommandHandler(entityID, support, sdkenumsettingv1.Handlers{
				Set: func(
					_ context.Context,
					typedCommand typed.Command[contractenumsettingv1.SetParameters],
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
			value, err := json.Marshal(parameters.Value)
			if err != nil {
				return plannedCommand{}, fmt.Errorf("encode power-on behavior: %w", err)
			}
			return plannedCommand{
				SetValues:     map[string]json.RawMessage{property: value},
				GetProperties: []string{property},
				Deadline:      deadline,
				Matches: func(report stateReport) bool {
					state, ok := report.semantic.(contractenumsettingv1.State)
					if !ok {
						return false
					}
					return contractenumsettingv1.SetSatisfied(parameters, state)
				},
			}, nil
		},
	}, nil
}

func powerOnBehaviorSupport(choices []string) contractenumsettingv1.Support {
	return contractenumsettingv1.Support{
		State:      contractenumsettingv1.StateSupport{Choices: choices},
		Operations: contractenumsettingv1.OperationSupport{Set: contractenumsettingv1.SetSupport{}},
	}
}

func decodePowerOnBehaviorState(
	payload json.RawMessage,
	choices []string,
) (contractenumsettingv1.State, error) {
	var value string
	if err := decodeJSON(payload, &value); err != nil {
		return "", fmt.Errorf("decode power-on behavior string: %w", err)
	}
	if !slices.Contains(choices, value) {
		return "", fmt.Errorf("power-on behavior value is outside its discovered choices")
	}
	return contractenumsettingv1.State(value), nil
}

// enumChoices validates dynamic enum choices against the shared string and
// choice bounds (1–128 chars per item, 1–64 unique items). Anything else
// omits the dependent expose without affecting valid siblings.
func enumChoices(values []string) ([]string, bool) {
	if len(values) == 0 || len(values) > maximumEnumChoices {
		return nil, false
	}
	seen := make(map[string]struct{}, len(values))
	choices := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || utf8.RuneCountInString(value) > maximumEnumChoiceRunes {
			return nil, false
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, false
		}
		seen[value] = struct{}{}
		choices = append(choices, value)
	}
	return choices, true
}
