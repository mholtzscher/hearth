package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkenumactionv1 "github.com/mholtzscher/hearth/sdk/adapter/enumactionv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const effectExposeName = "effect"

// maximumEnumChoices and maximumEnumChoiceRunes mirror the shared
// string/choice bounds (contracts/v1/common and registration-request
// bounds): 1–64 unique choices of 1–128 chars each.
const (
	maximumEnumChoices     = 64
	maximumEnumChoiceRunes = 128
)

// newEffectPlan builds the complete stateless effect action translation for
// one set-only property. The plan claims no State properties, runs no
// decoder, requests no refresh, and translates trigger Commands to
// dispatched plans: after /set PUBACK and acceptance the runtime releases
// the FIFO slot with no /get, no matcher, and no observation.
func newEffectPlan(
	metadata adapter.EntityMetadata,
	property string,
	values []string,
) (entityPlan, error) {
	support := effectSupport(values)
	descriptor, descriptorErr := sdkenumactionv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	return entityPlan{
		Descriptor:       descriptor,
		StatePolicy:      entityStateless,
		TranslateCommand: effectTranslator(property, support),
	}, nil
}

func effectSupport(values []string) sdkenumactionv1.Support {
	return sdkenumactionv1.Support{
		State: sdkenumactionv1.StateSupport{},
		Operations: sdkenumactionv1.OperationSupport{
			Trigger: sdkenumactionv1.TriggerSupport{Values: values},
		},
	}
}

func effectTranslator(
	property string,
	support sdkenumactionv1.Support,
) commandTranslator {
	return func(
		ctx context.Context,
		entityID string,
		command adapter.Command,
		responder adapter.Responder,
	) (plannedCommand, error) {
		var parameters sdkenumactionv1.TriggerParameters
		var deadline time.Time
		handler, err := sdkenumactionv1.NewCommandHandler(entityID, support, sdkenumactionv1.Handlers{
			Trigger: func(
				_ context.Context,
				typedCommand typed.Command[sdkenumactionv1.TriggerParameters],
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
		// Off-values fail here, before any MQTT publish, via the generated
		// membership validation against the discovered values.
		if err = handler(ctx, command, responder); err != nil {
			return plannedCommand{}, err
		}
		value, err := json.Marshal(parameters.Name)
		if err != nil {
			return plannedCommand{}, fmt.Errorf("encode effect: %w", err)
		}
		return plannedCommand{
			SetValues: map[string]json.RawMessage{property: value},
			Deadline:  deadline,
			Outcome:   plannedDispatched,
		}, nil
	}
}
