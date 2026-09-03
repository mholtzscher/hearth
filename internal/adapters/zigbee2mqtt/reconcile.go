package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type mappingKey struct {
	binding string
	entity  string
}

type runtimeEntity struct {
	discovered discoveredEntity
	entityID   string
}

type runtimeDevice struct {
	bindingKey  string
	ieeeAddress string
	friendly    string
	entities    []runtimeEntity
}

type routeSnapshot struct {
	routes  map[string]commandRoute
	devices map[string]runtimeDevice
}

func (z2m *Adapter) reconcile(
	ctx context.Context,
	generation uint64,
	connection mqttConnection,
	disconnect context.CancelCauseFunc,
	state *connectionSync,
) error {
	if state.inventory == nil {
		return nil
	}

	snapshot, err := z2m.buildRouteSnapshot(ctx, generation, *state.inventory)
	if err != nil {
		return err
	}
	revision, err := z2m.activateRoutes(ctx, generation, connection, disconnect, snapshot)
	if err != nil {
		return err
	}
	state.snapshot = snapshot
	state.routeRevision = revision
	if err = z2m.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		_ = z2m.invalidateRoutes(ctx, generation, err)
		state.snapshot = routeSnapshot{routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice)}
		state.routeRevision = 0
		return &sessionOperationError{operation: "report healthy Zigbee2MQTT bridge", err: err}
	}
	if err = z2m.replayPendingAvailability(ctx, state, snapshot); err != nil {
		return err
	}
	if err = z2m.replayPendingState(ctx, generation, state); err != nil {
		return err
	}
	return z2m.requestCurrentState(ctx, connection, *state.inventory, snapshot)
}

func (z2m *Adapter) activateRoutes(
	ctx context.Context,
	generation uint64,
	connection mqttConnection,
	disconnect context.CancelCauseFunc,
	snapshot routeSnapshot,
) (uint64, error) {
	result := make(chan routeActivationResult, 1)
	event := routesActivated{
		generation: generation,
		connection: connection,
		disconnect: disconnect,
		snapshot:   snapshot,
		result:     result,
	}
	select {
	case z2m.runtimeEvents <- event:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-z2m.runtimeDone:
		return 0, errors.New("Zigbee2MQTT runtime stopped")
	}
	select {
	case activation := <-result:
		return activation.revision, activation.err
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-z2m.runtimeDone:
		return 0, errors.New("Zigbee2MQTT runtime stopped")
	}
}

func (z2m *Adapter) buildRouteSnapshot(
	ctx context.Context,
	generation uint64,
	inventory inventoryDiscovery,
) (routeSnapshot, error) {
	snapshot := routeSnapshot{
		routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice),
	}
	for _, rejection := range inventory.Rejections {
		z2m.logger.WarnContext(
			ctx,
			"isolated Zigbee2MQTT Device",
			"ieee",
			rejection.IEEEAddress,
			"model",
			rejection.Model,
			"code",
			rejection.Code,
		)
	}
	if len(inventory.Devices) == 0 {
		z2m.logger.InfoContext(ctx, "Zigbee2MQTT inventory contains no eligible lights")
	}
	for _, device := range inventory.Devices {
		binding, registerErr := z2m.session.Register(ctx, device.Registration)
		if registerErr != nil {
			if rejected, ok := errors.AsType[*adapter.RegistrationRejectedError](registerErr); ok {
				z2m.logger.WarnContext(
					ctx,
					"Zigbee2MQTT Device registration rejected",
					"ieee",
					device.IEEEAddress,
					"model",
					device.Model,
					"code",
					rejected.Code,
				)
				continue
			}
			return routeSnapshot{}, &sessionOperationError{
				operation: "register Zigbee2MQTT Device",
				err:       registerErr,
			}
		}
		for _, entity := range binding.Entities {
			z2m.rememberMapping(adapter.OwnedMapping{
				BindingKey: binding.BindingKey,
				DeviceID:   binding.DeviceID,
				EntityKey:  entity.Key,
				EntityID:   entity.EntityID,
			})
		}
		runtime, runtimeErr := runtimeDeviceFromBinding(device, binding)
		if runtimeErr != nil {
			z2m.logger.WarnContext(
				ctx,
				"isolated invalid Zigbee2MQTT registration response",
				"ieee",
				device.IEEEAddress,
				"model",
				device.Model,
				"error",
				runtimeErr,
			)
			continue
		}
		snapshot.devices[runtime.friendly] = runtime
		for _, entity := range runtime.entities {
			route := commandRoute{
				entityID: entity.entityID, ieeeAddress: runtime.ieeeAddress, friendlyName: runtime.friendly,
				entity: entity.discovered, connectionGeneration: generation,
			}
			snapshot.routes[entity.entityID] = route
		}
	}
	return snapshot, nil
}

func (z2m *Adapter) replayPendingAvailability(
	ctx context.Context,
	state *connectionSync,
	snapshot routeSnapshot,
) error {
	for _, topic := range state.pendingOrder {
		message := state.pending[topic]
		if _, kind := classifyDeviceTopic(
			z2m.config.BaseTopic,
			message.Topic,
			snapshot.devices,
		); kind == deviceTopicAvailability {
			if err := z2m.cacheAvailability(state, message, snapshot.devices); err != nil {
				z2m.logger.WarnContext(
					ctx,
					"ignored invalid Zigbee2MQTT availability",
					"topic",
					message.Topic,
					"error",
					err,
				)
			}
		}
	}
	return z2m.reportReconciledAvailability(ctx, *state.inventory, snapshot, state.availability)
}

func (z2m *Adapter) replayPendingState(
	ctx context.Context,
	generation uint64,
	state *connectionSync,
) error {
	for _, topic := range state.pendingOrder {
		message := state.pending[topic]
		if _, kind := classifyDeviceTopic(
			z2m.config.BaseTopic,
			message.Topic,
			state.snapshot.devices,
		); kind == deviceTopicState {
			if err := z2m.processDeviceMessage(ctx, generation, state, message); err != nil {
				return err
			}
		}
	}
	state.clearPending()
	return nil
}

func (z2m *Adapter) requestCurrentState(
	ctx context.Context,
	connection mqttConnection,
	inventory inventoryDiscovery,
	snapshot routeSnapshot,
) error {
	for _, discovered := range inventory.Devices {
		device, exists := snapshot.devices[discovered.FriendlyName]
		if !exists {
			continue
		}
		for _, entity := range device.entities {
			if err := publishGet(
				ctx,
				connection,
				z2m.config.BaseTopic,
				device.friendly,
				entity.discovered.Property,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (z2m *Adapter) listOwnedMappings(ctx context.Context) ([]adapter.OwnedMapping, error) {
	var result []adapter.OwnedMapping
	cursor := ""
	seen := make(map[string]struct{})
	for {
		page, err := z2m.session.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{
			Limit: mappingPageLimit, Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		if page.NextCursor == "" {
			return result, nil
		}
		if page.NextCursor == cursor {
			return nil, errors.New("owned mapping pagination did not advance")
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return nil, errors.New("owned mapping pagination repeated a cursor")
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}

func runtimeDeviceFromBinding(device discoveredDevice, binding adapter.Binding) (runtimeDevice, error) {
	if binding.BindingKey != device.Registration.BindingKey {
		return runtimeDevice{}, errors.New("registration Binding key mismatch")
	}
	byKey := make(map[string]adapter.EntityBinding, len(binding.Entities))
	for _, entity := range binding.Entities {
		if entity.Key == "" || entity.EntityID == "" {
			return runtimeDevice{}, errors.New("registration returned an empty Entity mapping")
		}
		if _, duplicate := byKey[entity.Key]; duplicate {
			return runtimeDevice{}, errors.New("registration returned duplicate Entity keys")
		}
		byKey[entity.Key] = entity
	}
	runtime := runtimeDevice{
		bindingKey: device.Registration.BindingKey, ieeeAddress: device.IEEEAddress,
		friendly: device.FriendlyName, entities: make([]runtimeEntity, 0, len(device.Entities)),
	}
	for _, discovered := range device.Entities {
		mapped, exists := byKey[discovered.Descriptor.Key]
		if !exists {
			return runtimeDevice{}, fmt.Errorf("registration omitted Entity key %q", discovered.Descriptor.Key)
		}
		runtime.entities = append(runtime.entities, runtimeEntity{discovered: discovered, entityID: mapped.EntityID})
	}
	return runtime, nil
}

func (z2m *Adapter) rememberMapping(mapping adapter.OwnedMapping) {
	key := mappingKey{binding: mapping.BindingKey, entity: mapping.EntityKey}
	if _, exists := z2m.knownMappings[key]; !exists {
		z2m.knownOrder = append(z2m.knownOrder, key)
	}
	z2m.knownMappings[key] = mapping
}

func publishGet(ctx context.Context, connection mqttConnection, base, friendly, property string) error {
	payload, err := json.Marshal(map[string]string{property: ""})
	if err != nil {
		return err
	}
	return connection.Publish(ctx, base+"/"+friendly+"/get", mqttQoS, false, payload)
}
