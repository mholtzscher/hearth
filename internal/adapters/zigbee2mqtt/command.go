package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type commandRoute struct {
	entityID             string
	ieeeAddress          string
	friendlyName         string
	entity               runtimeEntity
	connectionGeneration uint64
	routeGeneration      uint64
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

// translateCommand delegates to the bound Entity plan, validates the planned
// output, and marshals every set value as one JSON object. A plan may write
// several properties and request several refresh properties.
func translateCommand(
	ctx context.Context,
	route commandRoute,
	command adapter.Command,
	responder adapter.Responder,
) ([]byte, plannedCommand, error) {
	if route.entity.plan.TranslateCommand == nil {
		return nil, plannedCommand{}, responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
	}
	planned, err := route.entity.plan.TranslateCommand(ctx, route.entityID, command, responder)
	if err != nil {
		return nil, plannedCommand{}, err
	}
	if err = validatePlannedCommand(planned); err != nil {
		return nil, plannedCommand{}, err
	}
	payload, err := json.Marshal(planned.SetValues)
	if err != nil {
		return nil, plannedCommand{}, err
	}
	return payload, planned, nil
}
