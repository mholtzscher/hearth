package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	contractenumsettingv1 "github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const powerOnBehaviorExposeName = "power_on_behavior"

// powerOnBehaviorCodecs shares immutable schemas across power-on behavior plans.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var powerOnBehaviorCodecs = sync.OnceValues(contractenumsettingv1.Compile)

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
			state, err := decodePowerOnBehaviorState(raw)
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

// decodePowerOnBehaviorState decodes one wire reading through the contract
// State codec, which owns JSON string shape. Choice membership is owned by
// NewObservation against the discovered support choices.
func decodePowerOnBehaviorState(payload json.RawMessage) (contractenumsettingv1.State, error) {
	codecs, err := powerOnBehaviorCodecs()
	if err != nil {
		return "", err
	}
	state, _, err := codecs.State.Decode(payload)
	if err != nil {
		return "", err
	}
	return state, nil
}
