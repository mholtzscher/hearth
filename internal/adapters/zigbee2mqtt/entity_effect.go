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

// newEnumActionPlan builds the complete stateless enum-action translation
// for one set-only property. The plan claims no State properties, runs no
// decoder, requests no refresh, and translates trigger Commands to
// dispatched plans: after /set PUBACK and acceptance the runtime releases
// the FIFO slot with no /get, no matcher, and no observation. Light effects
// and the smart-plug reset action share this constructor.
func newEnumActionPlan(
	metadata adapter.EntityMetadata,
	property string,
	values []string,
) (entityPlan, error) {
	support := enumActionSupport(values)
	descriptor, descriptorErr := sdkenumactionv1.NewEntityDescriptor(metadata, support)
	if descriptorErr != nil {
		return entityPlan{}, descriptorErr
	}
	return entityPlan{
		Descriptor:       descriptor,
		StatePolicy:      entityStateless,
		TranslateCommand: enumActionTranslator(property, support),
	}, nil
}

func enumActionSupport(values []string) sdkenumactionv1.Support {
	return sdkenumactionv1.Support{
		State: sdkenumactionv1.StateSupport{},
		Operations: sdkenumactionv1.OperationSupport{
			Trigger: sdkenumactionv1.TriggerSupport{Values: values},
		},
	}
}

func enumActionTranslator(
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
			return plannedCommand{}, fmt.Errorf("encode enum action: %w", err)
		}
		return plannedCommand{
			SetValues: map[string]json.RawMessage{property: value},
			Deadline:  deadline,
			Outcome:   plannedDispatched,
		}, nil
	}
}
