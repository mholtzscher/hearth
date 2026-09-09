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

// enumSettingCodecs shares immutable schemas across enum-setting plans.
//
//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative codecs.
var enumSettingCodecs = sync.OnceValues(contractenumsettingv1.Compile)

// newEnumSettingPlan builds the complete observable enum-setting translation
// for one State property. Choices come from the expose values and are never
// hard-coded; validation and satisfaction reuse the generated enumsetting
// behavior. Power-on behavior on lights and relays shares this constructor.
func newEnumSettingPlan(
	metadata adapter.EntityMetadata,
	property string,
	choices []string,
) (entityPlan, error) {
	support := enumSettingSupport(choices)
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
			state, err := decodeEnumSettingState(raw)
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
				return plannedCommand{}, fmt.Errorf("encode enum setting: %w", err)
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

func enumSettingSupport(choices []string) contractenumsettingv1.Support {
	return contractenumsettingv1.Support{
		State:      contractenumsettingv1.StateSupport{Choices: choices},
		Operations: contractenumsettingv1.OperationSupport{Set: contractenumsettingv1.SetSupport{}},
	}
}

// decodeEnumSettingState decodes one wire reading through the contract
// State codec, which owns JSON string shape. Choice membership is owned by
// NewObservation against the discovered support choices.
func decodeEnumSettingState(payload json.RawMessage) (contractenumsettingv1.State, error) {
	codecs, err := enumSettingCodecs()
	if err != nil {
		return "", err
	}
	state, _, err := codecs.State.Decode(payload)
	if err != nil {
		return "", err
	}
	return state, nil
}

// planPowerOnBehavior discovers the optional device-root power-on behavior
// setting once per device. It requires full publish/set/get access; the
// constructor validates the expose values against the shared choice bounds
// and omits the Entity on error. Light and relay families share this
// helper; each caller gates it on its own surviving power family.
func planPowerOnBehavior(input devicePlanningInput) *entityPlan {
	root, ok := input.Exposes.UniqueRoot(upstreamExposeEnum, powerOnBehaviorExposeName)
	if !ok || !root.resolved {
		return nil
	}
	expose := root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || !exposeCanSet(expose) ||
		!exposeCanGet(expose) || !input.Exposes.PropertyUnique(expose.Property) {
		return nil
	}
	key, name := scopedIdentity(
		"poweronbehavior",
		"Power-On Behavior",
		expose.Endpoint,
		root.endpoint,
		root.scoped,
	)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newEnumSettingPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/poweronbehavior",
		Name:       name,
	}, expose.Property, expose.Values)
	if err != nil {
		return nil
	}
	return &plan
}
