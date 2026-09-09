package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdknumericsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

// newNumericSettingPlan builds the complete observable numeric-setting
// translation for one State property in numeric value mode. Bounds come from
// the discovered expose, choices stay empty, and the optional unit carries
// the exact expected upstream unit when one is known. Fractions are
// preserved generically: the contract owns bounds while the wire uses plain
// JSON numbers, and satisfaction reuses the generated SetSatisfied behavior.
func newNumericSettingPlan(
	metadata adapter.EntityMetadata,
	property string,
	minimum, maximum float64,
	unit *string,
) (entityPlan, error) {
	support := numericSettingSupport(minimum, maximum, unit)
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
			state, err := decodeNumericSettingState(raw)
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
			if parameters.Value == nil {
				return plannedCommand{}, errors.New("numeric setting value is required")
			}
			wire, err := json.Marshal(*parameters.Value)
			if err != nil {
				return plannedCommand{}, fmt.Errorf("encode numeric setting: %w", err)
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

func numericSettingSupport(
	minimum, maximum float64,
	unit *string,
) contractnumericsettingv1.Support {
	return contractnumericsettingv1.Support{
		State: contractnumericsettingv1.StateSupport{
			Minimum: minimum,
			Maximum: maximum,
			Unit:    unit,
			Choices: []string{},
		},
		Operations: contractnumericsettingv1.OperationSupport{Set: contractnumericsettingv1.SetSupport{}},
	}
}

// decodeNumericSettingState maps one upstream JSON number to value-mode
// State. Only JSON numbers are accepted and fractions are preserved; bounds
// belong to the typed observation against the discovered support.
func decodeNumericSettingState(payload json.RawMessage) (contractnumericsettingv1.State, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return contractnumericsettingv1.State{}, fmt.Errorf("decode numeric setting number: %w", err)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return contractnumericsettingv1.State{}, errors.New("numeric setting value must be a JSON number")
	}
	value, err := number.Float64()
	if err != nil {
		return contractnumericsettingv1.State{}, fmt.Errorf("convert numeric setting number: %w", err)
	}
	if !isFinite(value) {
		return contractnumericsettingv1.State{}, errors.New("numeric setting value must be a finite number")
	}
	return contractnumericsettingv1.State{Mode: "value", Value: &value}, nil
}

// numericSettingSpec is one allowlisted observable numeric setting: the
// exact upstream expose name, Entity key/name, and the exact expected unit
// (nil means no unit expectation beyond an empty upstream unit).
type numericSettingSpec struct {
	exposeName   string
	key          string
	displayName  string
	expectedUnit string
	hasUnit      bool
}

// numericSettingBounds validates the exact discovered bounds of a numeric
// setting feature: both bounds must be present, finite, and ordered with
// minimum < maximum. A missing, malformed, one-sided, or inverted bound
// makes only that setting ineligible.
func numericSettingBounds(feature upstreamExpose) (float64, float64, bool) {
	if feature.ValueMin == nil || feature.ValueMax == nil {
		return 0, 0, false
	}
	minimum, maximum := *feature.ValueMin, *feature.ValueMax
	if !isFinite(minimum) || !isFinite(maximum) || minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

// planNumericSetting discovers one allowlisted observable numeric setting
// once per device. The expose must be a resolved unique root with a
// device-unique nonempty property, full publish/set/get access, the exact
// expected unit, and exact discovered bounds. Ineligible siblings are
// omitted without affecting valid settings.
func planNumericSetting(input devicePlanningInput, spec numericSettingSpec) *entityPlan {
	root, ok := input.Exposes.UniqueRoot(upstreamExposeNumeric, spec.exposeName)
	if !ok || !root.resolved {
		return nil
	}
	expose := root.expose
	if expose.Property == "" || !exposeCanPublish(expose) || !exposeCanSet(expose) ||
		!exposeCanGet(expose) || !input.Exposes.PropertyUnique(expose.Property) {
		return nil
	}
	if spec.hasUnit {
		if expose.Unit != spec.expectedUnit {
			return nil
		}
	} else if expose.Unit != "" {
		return nil
	}
	minimum, maximum, valid := numericSettingBounds(expose)
	if !valid {
		return nil
	}
	key, name := scopedIdentity(spec.key, spec.displayName, expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	var unit *string
	if spec.hasUnit {
		unitValue := spec.expectedUnit
		unit = &unitValue
	}
	plan, err := newNumericSettingPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/" + spec.key,
		Name:       name,
	}, expose.Property, minimum, maximum, unit)
	if err != nil {
		return nil
	}
	return &plan
}

// smartPlugNumericSettings lists the allowlisted smart-plug numeric
// settings in deterministic planner order.
func smartPlugNumericSettings() []numericSettingSpec {
	return []numericSettingSpec{
		{
			exposeName: "led_brightness", key: "ledbrightness", displayName: "LED Brightness",
			expectedUnit: "%", hasUnit: true,
		},
		{
			exposeName: "countdown_to_turn_off", key: "countdowntoturnoff",
			displayName: "Countdown To Turn Off", expectedUnit: "s", hasUnit: true,
		},
		{
			exposeName: "countdown_to_turn_on", key: "countdowntoturnon",
			displayName: "Countdown To Turn On", expectedUnit: "s", hasUnit: true,
		},
	}
}
