package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

//nolint:gocognit,nestif // The connection state machine keeps snapshot and relay ordering explicit.
func (z2m *Adapter) runConnection(
	ctx context.Context,
	generation uint64,
	progress *connectionProgress,
) (bool, error) {
	owned, err := z2m.listOwnedMappings(ctx)
	if err != nil {
		return false, &sessionOperationError{operation: "list owned Zigbee2MQTT mappings", err: err}
	}
	for _, mapping := range owned {
		z2m.rememberMapping(mapping)
	}

	messages := make(chan mqttMessage, messageChannelBuffer)
	synchronized := false
	connectionContext, cancelConnection := context.WithCancelCause(ctx)
	connection, err := z2m.dialer.Dial(
		connectionContext,
		mqttConfig{URL: z2m.config.MQTTURL, ClientID: z2m.config.ClientID},
		func(message mqttMessage) {
			select {
			case messages <- message:
			case <-connectionContext.Done():
			}
		},
	)
	if err != nil {
		cancelConnection(nil)
		return false, err
	}
	defer func() {
		_ = z2m.invalidateRoutes(ctx, generation, context.Cause(connectionContext))
		cancelConnection(nil)
		connection.Close()
	}()
	go z2m.monitorConnectionLoss(connectionContext, cancelConnection, connection)
	if err = connection.Subscribe(connectionContext, z2m.config.BaseTopic+"/#", mqttQoS); err != nil {
		return synchronized, preferConnectionError(ctx, connectionContext, err)
	}

	state := connectionSync{
		pending:      make(map[string]mqttMessage),
		availability: make(map[string]availabilityEvidence),
		snapshot:     routeSnapshot{routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice)},
	}
	for {
		if state.ready() && state.dirty {
			for draining := true; draining; {
				select {
				case message := <-messages:
					message = normalizeReceivedMessage(message)
					if err = z2m.ingestMessage(connectionContext, generation, &state, message, progress); err != nil {
						return synchronized, preferConnectionError(ctx, connectionContext, err)
					}
					if !state.ready() {
						draining = false
					}
				default:
					draining = false
				}
			}
			if state.ready() && state.dirty {
				if err = z2m.reconcile(
					connectionContext,
					generation,
					connection,
					cancelConnection,
					&state,
					progress,
				); err != nil {
					return synchronized, preferConnectionError(ctx, connectionContext, err)
				}
				synchronized = true
				state.dirty = false
			}
		}
		select {
		case <-ctx.Done():
			return synchronized, ctx.Err()
		case <-connectionContext.Done():
			return synchronized, preferConnectionError(ctx, connectionContext, connectionContext.Err())
		case message := <-messages:
			message = normalizeReceivedMessage(message)
			if err = z2m.ingestMessage(connectionContext, generation, &state, message, progress); err != nil {
				return synchronized, preferConnectionError(ctx, connectionContext, err)
			}
		}
	}
}

func (z2m *Adapter) monitorConnectionLoss(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	connection mqttConnection,
) {
	select {
	case err := <-connection.Lost():
		if err == nil {
			err = errors.New("MQTT connection lost")
		}
		cancel(err)
	case <-ctx.Done():
	}
}

func preferConnectionError(
	parent context.Context,
	connectionContext context.Context,
	operationErr error,
) error {
	if parent.Err() == nil && connectionContext.Err() != nil {
		return context.Cause(connectionContext)
	}
	return operationErr
}

type connectionSync struct {
	hasBridgeState bool
	bridgeOnline   bool
	info           *bridgeInfo
	inventory      *inventoryDiscovery
	dirty          bool
	pendingOrder   []string
	pending        map[string]mqttMessage
	availability   map[string]availabilityEvidence
	snapshot       routeSnapshot
	routeRevision  uint64
}

type availabilityEvidence struct {
	available  bool
	receivedAt time.Time
}

func normalizeReceivedMessage(message mqttMessage) mqttMessage {
	if message.ReceivedAt.IsZero() {
		message.ReceivedAt = time.Now().UTC()
	} else {
		message.ReceivedAt = message.ReceivedAt.UTC()
	}
	return message
}

func (state *connectionSync) ready() bool {
	return state.hasBridgeState && state.bridgeOnline && state.info != nil && compatibleBridgeInfo(*state.info) &&
		state.inventory != nil
}

//nolint:gocognit // Bridge document validity and readiness are one explicit state transition table.
func (z2m *Adapter) ingestMessage(
	ctx context.Context,
	generation uint64,
	state *connectionSync,
	message mqttMessage,
	progress *connectionProgress,
) error {
	switch classifyBridgeTopic(z2m.config.BaseTopic, message.Topic) {
	case bridgeTopicState:
		var bridge bridgeState
		if err := decodeJSON(
			message.Payload,
			&bridge,
		); err != nil ||
			(bridge.State != upstreamOnline && bridge.State != upstreamOffline) {
			state.hasBridgeState = false
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, progress, state)
		}
		state.hasBridgeState = true
		state.bridgeOnline = bridge.State == upstreamOnline
		if !state.bridgeOnline {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, bridgeOfflineReason, progress, state)
		}
		if state.info != nil && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicInfo:
		info, err := decodeBridgeInfo(message.Payload)
		if err != nil {
			state.info = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, progress, state)
		}
		state.info = &info
		z2m.logger.DebugContext(ctx, "received Zigbee2MQTT bridge information", eventKey, "adapter.bridge_info")
		if !compatibleBridgeInfo(info) {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, incompatibleConfigurationReason, progress, state)
		}
		if state.hasBridgeState && state.bridgeOnline && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicDevices:
		inventory, err := discoverInventory(message.Payload)
		if err != nil {
			state.inventory = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, progress, state)
		}
		if err = z2m.invalidateRoutes(ctx, generation, nil); err != nil {
			return err
		}
		state.snapshot = routeSnapshot{routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice)}
		state.routeRevision = 0
		state.inventory = &inventory
		state.dirty = true
	case bridgeTopicEvent:
		var event map[string]json.RawMessage
		if err := decodeJSON(message.Payload, &event); err != nil {
			z2m.logger.WarnContext(
				ctx,
				"ignored malformed Zigbee2MQTT bridge event",
				eventKey, "adapter.bridge_event_ignored",
				"error_code", "invalid_bridge_event",
			)
		}
	case bridgeTopicUnknown:
		if state.routeRevision != 0 {
			return z2m.processDeviceMessage(ctx, generation, state, message)
		}
		if queueableDeviceTopic(z2m.config.BaseTopic, message.Topic, state.inventory) {
			state.queuePending(message)
		}
	}
	return nil
}

func compatibleBridgeInfo(info bridgeInfo) bool {
	return info.MQTTVersion == mqttProtocolVersion311 && info.AvailabilityEnabled && !info.Optimistic
}

func (state *connectionSync) queuePending(message mqttMessage) {
	if state.pending == nil {
		state.pending = make(map[string]mqttMessage)
	}
	if _, exists := state.pending[message.Topic]; !exists {
		state.pendingOrder = append(state.pendingOrder, message.Topic)
	}
	state.pending[message.Topic] = message
}

func (state *connectionSync) clearPending() {
	state.pendingOrder = nil
	clear(state.pending)
}

func (z2m *Adapter) reportUnhealthy(
	ctx context.Context,
	generation uint64,
	reason string,
	progress *connectionProgress,
	states ...*connectionSync,
) error {
	if progress != nil {
		progress.upstreamReady = false
	}
	if err := z2m.invalidateRoutes(ctx, generation, errors.New(reason)); err != nil {
		return err
	}
	for _, state := range states {
		state.snapshot = routeSnapshot{routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice)}
		state.routeRevision = 0
	}
	if err := z2m.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthUnhealthy, SourceObservedAt: time.Now().UTC(), ReasonCode: reason,
	}); err != nil {
		return &sessionOperationError{operation: "report unhealthy Zigbee2MQTT bridge", err: err}
	}
	return nil
}

func (z2m *Adapter) invalidateRoutes(ctx context.Context, generation uint64, cause error) error {
	result := make(chan error, 1)
	event := routesInvalidated{generation: generation, cause: cause, result: result}
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
