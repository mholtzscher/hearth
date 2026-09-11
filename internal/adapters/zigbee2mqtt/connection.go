package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

//nolint:gocognit,nestif // The connection state machine keeps snapshot and relay ordering explicit.
func (z2m *Adapter) runConnection(ctx context.Context, generation uint64) (bool, error) {
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
		availability: make(map[string]availabilityEvidence),
		snapshot:     routeSnapshot{routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice)},
	}
	for {
		if state.ready() && state.dirty {
			for draining := true; draining; {
				select {
				case message := <-messages:
					message = normalizeReceivedMessage(message)
					if err = z2m.ingestMessage(connectionContext, generation, &state, message); err != nil {
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
			if err = z2m.ingestMessage(connectionContext, generation, &state, message); err != nil {
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

// pendingMessageLimit bounds the connection-local queue of device messages
// that arrived before route activation, counted in entries rather than
// occurrences so unique ordinary topics cannot grow it without limit. It is
// deliberately generous: a Zigbee2MQTT broker replays retained State and
// availability for every Device on subscribe (two topics per Device), so 1024
// entries cover a 512-Device network, far beyond a practical Zigbee mesh, and
// still tolerate hundreds of queued Event occurrences. The bound keeps memory
// at entries times a device-sized MQTT payload, on the order of a few hundred
// kibibytes, and keeps the ordinary-state coalescing scan O(n) over a bounded
// queue. Exceeding it is treated as a stalled connection, not as a reason to
// discard one arbitrarily chosen message.
const pendingMessageLimit = 1024

// errPendingMessageLimit ends the current connection generation when admitting
// one more pending message would exceed pendingMessageLimit. It is a plain
// sentinel rather than a sessionOperationError so the retry loop reports the
// adapter unhealthy, releases the generation-local queue, and reconnects with
// existing backoff instead of terminating the process. It carries no topic,
// payload, or device identity.
var errPendingMessageLimit = errors.New(
	"Zigbee2MQTT pending message limit reached; reconnecting to resynchronize",
)

type connectionSync struct {
	hasBridgeState bool
	bridgeOnline   bool
	info           *bridgeInfo
	inventory      *inventoryDiscovery
	dirty          bool
	// pending holds every device message received before route activation, in
	// replay order, bounded by pendingMessageLimit. An occurrence entry is
	// always kept individually; every other entry coalesces to the latest
	// message per topic so a stalled connection never replays stale State and
	// availability history.
	pending       []pendingMessage
	availability  map[string]availabilityEvidence
	snapshot      routeSnapshot
	routeRevision uint64
}

// pendingMessage is one queued upstream message awaiting route activation.
// occurrence marks a message that may carry an Entity Event occurrence and
// therefore must replay individually, in arrival order, exactly once. Every
// other queued message coalesces with later messages on the same topic.
type pendingMessage struct {
	message    mqttMessage
	occurrence bool
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
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, state)
		}
		state.hasBridgeState = true
		state.bridgeOnline = bridge.State == upstreamOnline
		if !state.bridgeOnline {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, bridgeOfflineReason, state)
		}
		if state.info != nil && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicInfo:
		info, err := decodeBridgeInfo(message.Payload)
		if err != nil {
			state.info = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, state)
		}
		state.info = &info
		z2m.logger.DebugContext(
			ctx,
			"received Zigbee2MQTT bridge information",
			slog.String(eventKey, "adapter.bridge_info"),
		)
		if !compatibleBridgeInfo(info) {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, incompatibleConfigurationReason, state)
		}
		if state.hasBridgeState && state.bridgeOnline && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicDevices:
		inventory, err := discoverInventory(message.Payload)
		if err != nil {
			state.inventory = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, generation, invalidInventoryReason, state)
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
				slog.String(eventKey, "adapter.bridge_event_ignored"),
				slog.String("error_code", "invalid_bridge_event"),
			)
		}
	case bridgeTopicUnknown:
		if state.routeRevision != 0 {
			return z2m.processDeviceMessage(ctx, generation, state, message)
		}
		if queueableDeviceTopic(z2m.config.BaseTopic, message.Topic, state.inventory) {
			if err := state.queuePending(message, z2m.carriesEventOccurrence(state, message)); err != nil {
				return z2m.rejectPendingOverflow(ctx, err)
			}
		}
	}
	return nil
}

// rejectPendingOverflow emits one fixed diagnostic at the pending-limit
// decision and returns the limit error so the connection generation ends. Only
// the bound and its stable code are logged: never the topic, payload, or
// device.
func (z2m *Adapter) rejectPendingOverflow(ctx context.Context, cause error) error {
	z2m.logger.WarnContext(
		ctx,
		"Zigbee2MQTT pending message limit reached",
		slog.String(eventKey, "adapter.pending_message_limit_reached"),
		slog.String("error_code", pendingMessageLimitErrorCode),
		slog.Int("pending_limit", pendingMessageLimit),
	)
	return cause
}

func compatibleBridgeInfo(info bridgeInfo) bool {
	return info.MQTTVersion == mqttProtocolVersion311 && info.AvailabilityEnabled && !info.Optimistic
}

// queuePending records one device message received before route activation.
// An occurrence is always appended individually so two equal action reports
// stay two occurrences, and so a later unrelated report on the same topic can
// never erase one. Any other message replaces the queued message for its
// topic and moves to the end of the queue, so the latest State per topic still
// replays last without keeping unbounded history for every topic.
//
// The queue is bounded by pendingMessageLimit. Coalescing an existing ordinary
// entry for the same topic replaces it in place and so never increases the
// entry count, which keeps replay working at the bound. Only an occurrence or
// a new ordinary topic would grow the queue, and when that would exceed the
// bound queuePending returns errPendingMessageLimit without touching the
// queue: the caller must end the connection generation rather than pick one
// message to evict, because silently dropping a chosen Event occurrence would
// lose or reorder an upstream event with no operator-visible signal.
func (state *connectionSync) queuePending(message mqttMessage, occurrence bool) error {
	if occurrence {
		return state.appendPending(pendingMessage{message: message, occurrence: true})
	}
	for index, entry := range state.pending {
		if entry.occurrence || entry.message.Topic != message.Topic {
			continue
		}
		state.pending = append(state.pending[:index], state.pending[index+1:]...)
		state.pending = append(state.pending, pendingMessage{message: message})
		return nil
	}
	return state.appendPending(pendingMessage{message: message})
}

// appendPending admits one new entry only while the queue is below
// pendingMessageLimit, so a refused append never mutates order or content.
func (state *connectionSync) appendPending(entry pendingMessage) error {
	if len(state.pending) >= pendingMessageLimit {
		return errPendingMessageLimit
	}
	state.pending = append(state.pending, entry)
	return nil
}

// carriesEventOccurrence reports whether one not-yet-activated device message
// may carry an Entity Event occurrence. A retained message is a cached replay,
// never an occurrence. Before inventory arrives the adapter cannot know which
// properties an Entity owns, so it conservatively treats a top-level action or
// action_* property as event-bearing; once inventory exists it uses the
// discovered Event property ownership instead of guessing.
func (z2m *Adapter) carriesEventOccurrence(state *connectionSync, message mqttMessage) bool {
	if message.Retained {
		return false
	}
	friendly, kind := parseDeviceTopic(z2m.config.BaseTopic, message.Topic)
	if kind != deviceTopicState {
		return false
	}
	properties, err := decodeDeviceProperties(message.Payload)
	if err != nil {
		return false
	}
	if state.inventory == nil {
		return hasConservativeActionProperty(properties)
	}
	for _, device := range state.inventory.Devices {
		if device.FriendlyName == friendly {
			return deviceOwnsEventProperty(device, properties)
		}
	}
	return false
}

// hasConservativeActionProperty is the pre-inventory occurrence test. It
// deliberately over-approximates by accepting every action or action_*
// property, because an unknown device could own any such Event source.
func hasConservativeActionProperty(properties map[string]json.RawMessage) bool {
	for property := range properties {
		if isActionEventProperty(property) {
			return true
		}
	}
	return false
}

// deviceOwnsEventProperty is the post-inventory occurrence test: exactly the
// properties of decoded Event plans count, so a message that only carries a
// State or Command property still coalesces by topic.
func deviceOwnsEventProperty(device discoveredDevice, properties map[string]json.RawMessage) bool {
	for _, entity := range device.Entities {
		if entity.DecodeEvent == nil {
			continue
		}
		for _, property := range entity.EventProperties {
			if _, present := properties[property]; present {
				return true
			}
		}
	}
	return false
}

func (state *connectionSync) clearPending() {
	state.pending = nil
}

func (z2m *Adapter) reportUnhealthy(
	ctx context.Context,
	generation uint64,
	reason string,
	states ...*connectionSync,
) error {
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
