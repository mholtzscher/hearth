package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

type commandRoute struct {
	entityID             string
	ieeeAddress          string
	friendlyName         string
	entity               discoveredEntity
	connectionGeneration uint64
	routeGeneration      uint64
}

type desiredState struct {
	power      bool
	brightness int64
}

type matchedState struct {
	state      decodedEntityState
	receivedAt time.Time
}

func (z2m *Adapter) HandleCommand(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	result := make(chan error, 1)
	event := commandSubmitted{ctx: ctx, command: command, responder: responder, result: result}
	select {
	case z2m.runtimeEvents <- event:
	case <-ctx.Done():
		return ctx.Err()
	case <-z2m.runtimeDone:
		return errors.New("Zigbee2MQTT runtime stopped")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-z2m.runtimeDone:
		return errors.New("Zigbee2MQTT runtime stopped")
	}
}

func translateCommand(
	ctx context.Context,
	route commandRoute,
	command adapter.Command,
	responder adapter.Responder,
) ([]byte, desiredState, time.Time, error) {
	var (
		value    json.RawMessage
		desired  desiredState
		deadline time.Time
	)
	switch route.entity.Kind {
	case entityKindPower:
		handler, err := sdkpowerv1.NewCommandHandler(route.entityID, powerSupport(), sdkpowerv1.Handlers{
			Set: func(
				_ context.Context,
				typedCommand typed.Command[contractpowerv1.SetParameters],
				_ adapter.Responder,
			) error {
				translated, valueErr := powerCommandValue(route.entity, typedCommand.Parameters.Value)
				if valueErr != nil {
					return valueErr
				}
				value = translated
				desired.power = typedCommand.Parameters.Value
				deadline = typedCommand.Deadline
				return nil
			},
		})
		if err != nil {
			return nil, desiredState{}, time.Time{}, err
		}
		if err = handler(ctx, command, responder); err != nil {
			return nil, desiredState{}, time.Time{}, err
		}
	case entityKindBrightness:
		handler, err := sdkbrightnessv1.NewCommandHandler(route.entityID, brightnessSupport(), sdkbrightnessv1.Handlers{
			Set: func(
				_ context.Context,
				typedCommand typed.Command[contractbrightnessv1.SetParameters],
				_ adapter.Responder,
			) error {
				translated, valueErr := brightnessCommandValue(route.entity, typedCommand.Parameters.Value)
				if valueErr != nil {
					return valueErr
				}
				value = translated
				desired.brightness = typedCommand.Parameters.Value
				deadline = typedCommand.Deadline
				return nil
			},
		})
		if err != nil {
			return nil, desiredState{}, time.Time{}, err
		}
		if err = handler(ctx, command, responder); err != nil {
			return nil, desiredState{}, time.Time{}, err
		}
	default:
		return nil, desiredState{}, time.Time{}, responder.RejectUnavailable(
			"Zigbee2MQTT Entity is unavailable",
		)
	}
	payload, err := json.Marshal(map[string]json.RawMessage{route.entity.Property: value})
	if err != nil {
		return nil, desiredState{}, time.Time{}, err
	}
	return payload, desired, deadline, nil
}
