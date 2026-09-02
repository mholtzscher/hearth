package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	reconnectMinimum     = 250 * time.Millisecond
	reconnectMaximum     = 5 * time.Second
	mqttQoS              = byte(1)
	mappingPageLimit     = 200
	availabilityPage     = 256
	messageChannelBuffer = 64
	jitterDivisor        = 2

	externalSystemUnavailableReason = "hearth.external_system_unavailable"
	bridgeOfflineReason             = "adapter.hearth-adapter-zigbee2mqtt.bridge_offline"
	incompatibleConfigurationReason = "adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration"
	invalidInventoryReason          = "adapter.hearth-adapter-zigbee2mqtt.invalid_inventory"
	deviceOfflineReason             = "adapter.hearth-adapter-zigbee2mqtt.device_offline"
	deviceMissingReason             = "adapter.hearth-adapter-zigbee2mqtt.device_missing"
	deviceDisabledReason            = "adapter.hearth-adapter-zigbee2mqtt.device_disabled"
	capabilityMissingReason         = "adapter.hearth-adapter-zigbee2mqtt.capability_missing"
)

type Session interface {
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
	MQTTURL   string
	BaseTopic string
	ClientID  string
}

type Adapter struct {
	session Session
	config  Config
	logger  *slog.Logger
	dialer  mqttDialer

	routeLifecycle   sync.RWMutex
	mutex            sync.Mutex
	connection       mqttConnection
	connectionCancel context.CancelCauseFunc
	generation       uint64
	healthy          bool
	routeSerial      uint64
	routes           map[string]commandRoute
	devices          map[string]runtimeDevice
	matchers         map[string]*commandMatcher
	deviceLocks      map[string]*contextLock

	knownMappings map[mappingKey]adapter.OwnedMapping
	knownOrder    []mappingKey
	retryDelay    func(time.Duration) time.Duration
}

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

type sessionOperationError struct {
	operation string
	err       error
}

func (err *sessionOperationError) Error() string { return err.operation + ": " + err.err.Error() }
func (err *sessionOperationError) Unwrap() error { return err.err }

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error) {
	return newAdapter(session, config, logger, newPahoDialer())
}

func newAdapter(session Session, config Config, logger *slog.Logger, dialer mqttDialer) (*Adapter, error) {
	switch {
	case session == nil:
		return nil, errors.New("Zigbee2MQTT adapter Session is required")
	case strings.TrimSpace(config.MQTTURL) == "":
		return nil, errors.New("Zigbee2MQTT MQTT URL is required")
	case !validRouteSlug(config.BaseTopic):
		return nil, errors.New("Zigbee2MQTT base topic must be a route-safe slug")
	case strings.TrimSpace(config.ClientID) == "":
		return nil, errors.New("Zigbee2MQTT MQTT client ID is required")
	case dialer == nil:
		return nil, errors.New("Zigbee2MQTT MQTT dialer is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		session: session, config: config, logger: logger, dialer: dialer,
		routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice),
		matchers: make(map[string]*commandMatcher), deviceLocks: make(map[string]*contextLock),
		knownMappings: make(map[mappingKey]adapter.OwnedMapping),
		retryDelay:    jitterReconnect,
	}, nil
}

func (z2m *Adapter) Run(ctx context.Context) error {
	delay := reconnectMinimum
	for {
		synchronized, err := z2m.runConnection(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Parent cancellation is graceful shutdown.
		}
		if _, ok := errors.AsType[*sessionOperationError](err); ok {
			return err
		}
		if healthErr := z2m.reportUnhealthy(ctx, externalSystemUnavailableReason); healthErr != nil {
			return healthErr
		}
		if synchronized {
			delay = reconnectMinimum
		}
		wait := z2m.retryDelay(delay)
		z2m.logger.WarnContext(ctx, "Zigbee2MQTT connection ended; reconnecting", "error", err, "retry_in", wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if delay < reconnectMaximum {
			delay *= 2
			if delay > reconnectMaximum {
				delay = reconnectMaximum
			}
		}
	}
}

//nolint:gocognit,nestif // The connection state machine keeps snapshot and relay ordering explicit.
func (z2m *Adapter) runConnection(ctx context.Context) (bool, error) {
	owned, err := z2m.listOwnedMappings(ctx)
	if err != nil {
		return false, &sessionOperationError{operation: "list owned Zigbee2MQTT mappings", err: err}
	}
	for _, mapping := range owned {
		z2m.rememberMapping(mapping)
	}

	messages := make(chan mqttMessage, messageChannelBuffer)
	synchronized := false
	z2m.mutex.Lock()
	z2m.generation++
	generation := z2m.generation
	z2m.mutex.Unlock()

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
		cancelConnection(nil)
		connection.Close()
	}()
	go z2m.monitorConnectionLoss(connectionContext, cancelConnection, connection)
	z2m.setConnection(generation, connection, cancelConnection)
	defer z2m.clearConnection(generation)
	if err = connection.Subscribe(connectionContext, z2m.config.BaseTopic+"/#", mqttQoS); err != nil {
		return synchronized, preferConnectionError(ctx, connectionContext, err)
	}

	state := connectionSync{
		pending:      make(map[string]mqttMessage),
		availability: make(map[string]availabilityEvidence),
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
				if err = z2m.reconcile(connectionContext, generation, connection, &state); err != nil {
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
		z2m.disableRoutes()
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
			return z2m.reportUnhealthy(ctx, invalidInventoryReason)
		}
		state.hasBridgeState = true
		state.bridgeOnline = bridge.State == upstreamOnline
		if !state.bridgeOnline {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, bridgeOfflineReason)
		}
		if state.info != nil && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicInfo:
		info, err := decodeBridgeInfo(message.Payload)
		if err != nil {
			state.info = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, invalidInventoryReason)
		}
		state.info = &info
		z2m.logger.InfoContext(ctx, "received Zigbee2MQTT bridge information", "version", info.Version)
		if !compatibleBridgeInfo(info) {
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, incompatibleConfigurationReason)
		}
		if state.hasBridgeState && state.bridgeOnline && state.inventory != nil {
			state.dirty = true
		}
	case bridgeTopicDevices:
		inventory, err := discoverInventory(message.Payload)
		if err != nil {
			state.inventory = nil
			z2m.clearAvailabilityEvidence(state)
			return z2m.reportUnhealthy(ctx, invalidInventoryReason)
		}
		z2m.disableRoutes()
		state.inventory = &inventory
		state.dirty = true
	case bridgeTopicEvent:
		var event map[string]json.RawMessage
		if err := decodeJSON(message.Payload, &event); err != nil {
			z2m.logger.WarnContext(ctx, "ignored malformed Zigbee2MQTT bridge event", "error", err)
		}
	case bridgeTopicUnknown:
		if z2m.isHealthy(generation) {
			return z2m.processDeviceMessage(ctx, generation, state, message)
		}
		if queueableDeviceTopic(z2m.config.BaseTopic, message.Topic, state.inventory) {
			state.queuePending(message)
		}
	}
	return nil
}

func decodeBridgeInfo(payload []byte) (bridgeInfo, error) {
	var wire struct {
		Version *string `json:"version"`
		Config  *struct {
			MQTT *struct {
				Version *int `json:"version"`
			} `json:"mqtt"`
			Availability *struct {
				Enabled *bool `json:"enabled"`
			} `json:"availability"`
			DeviceOptions *struct {
				Optimistic *bool `json:"optimistic"`
			} `json:"device_options"`
		} `json:"config"`
	}
	if err := decodeJSON(payload, &wire); err != nil {
		return bridgeInfo{}, err
	}
	if wire.Version == nil || wire.Config == nil || wire.Config.MQTT == nil || wire.Config.MQTT.Version == nil ||
		wire.Config.Availability == nil || wire.Config.Availability.Enabled == nil ||
		wire.Config.DeviceOptions == nil || wire.Config.DeviceOptions.Optimistic == nil {
		return bridgeInfo{}, errors.New("bridge info is missing required fields")
	}
	if *wire.Version == "" {
		return bridgeInfo{}, errors.New("bridge info version is required")
	}
	return bridgeInfo{
		Version:             *wire.Version,
		MQTTVersion:         *wire.Config.MQTT.Version,
		AvailabilityEnabled: *wire.Config.Availability.Enabled,
		Optimistic:          *wire.Config.DeviceOptions.Optimistic,
	}, nil
}

func compatibleBridgeInfo(info bridgeInfo) bool {
	return info.MQTTVersion == mqttProtocolVersion311 && info.AvailabilityEnabled && !info.Optimistic
}

//nolint:gocognit,funlen // Registration and ordered evidence form one transaction-like reconciliation flow.
func (z2m *Adapter) reconcile(
	ctx context.Context,
	generation uint64,
	connection mqttConnection,
	state *connectionSync,
) error {
	if state.inventory == nil {
		return nil
	}
	var err error
	snapshot := routeSnapshot{
		routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice),
	}
	for _, rejection := range state.inventory.Rejections {
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
	if len(state.inventory.Devices) == 0 {
		z2m.logger.InfoContext(ctx, "Zigbee2MQTT inventory contains no eligible lights")
	}
	for _, device := range state.inventory.Devices {
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
			return &sessionOperationError{operation: "register Zigbee2MQTT Device", err: registerErr}
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

	wasHealthy := z2m.isHealthy(generation)
	if !wasHealthy {
		if err = z2m.session.SetHealth(ctx, adapter.HealthReport{
			Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
		}); err != nil {
			return &sessionOperationError{operation: "report healthy Zigbee2MQTT bridge", err: err}
		}
	}
	z2m.installSnapshot(generation, snapshot)

	for _, topic := range state.pendingOrder {
		message := state.pending[topic]
		if _, kind := classifyDeviceTopic(
			z2m.config.BaseTopic,
			message.Topic,
			snapshot.devices,
		); kind == deviceTopicAvailability {
			if err = z2m.cacheAvailability(state, message, snapshot.devices); err != nil {
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
	if err = z2m.reportReconciledAvailability(ctx, *state.inventory, snapshot, state.availability); err != nil {
		return err
	}
	for _, topic := range state.pendingOrder {
		message := state.pending[topic]
		if _, kind := classifyDeviceTopic(
			z2m.config.BaseTopic,
			message.Topic,
			snapshot.devices,
		); kind == deviceTopicState {
			if err = z2m.processDeviceMessage(ctx, generation, state, message); err != nil {
				return err
			}
		}
	}
	state.clearPending()
	for _, discovered := range state.inventory.Devices {
		device, exists := snapshot.devices[discovered.FriendlyName]
		if !exists {
			continue
		}
		for _, entity := range device.entities {
			if err = publishGet(
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

//nolint:gocognit // The reason precedence directly mirrors the owned-mapping reconciliation contract.
func (z2m *Adapter) reportReconciledAvailability(
	ctx context.Context,
	inventory inventoryDiscovery,
	snapshot routeSnapshot,
	evidence map[string]availabilityEvidence,
) error {
	currentIEEE := make(map[mappingKey]string)
	for _, device := range snapshot.devices {
		for _, entity := range device.entities {
			currentIEEE[mappingKey{binding: device.bindingKey, entity: entity.discovered.Descriptor.Key}] =
				device.ieeeAddress
		}
	}
	present := make(map[string]struct{})
	disabled := make(map[string]struct{})
	for _, device := range inventory.Devices {
		present[device.Registration.BindingKey] = struct{}{}
	}
	for _, rejection := range inventory.Rejections {
		if rejection.IEEEAddress == "" {
			continue
		}
		binding := "z2m-" + strings.TrimPrefix(rejection.IEEEAddress, "0x")
		present[binding] = struct{}{}
		if rejection.Code == rejectionDisabled {
			disabled[binding] = struct{}{}
		}
	}

	reports := make([]adapter.EntityAvailabilityReport, 0, len(z2m.knownOrder))
	for _, key := range z2m.knownOrder {
		mapping := z2m.knownMappings[key]
		report := adapter.EntityAvailabilityReport{EntityID: mapping.EntityID, SourceObservedAt: time.Now().UTC()}
		ieeeAddress, current := currentIEEE[key]
		if !current {
			report.Status = adapter.AvailabilityUnavailable
			if _, isDisabled := disabled[key.binding]; isDisabled {
				report.ReasonCode = deviceDisabledReason
			} else if _, isPresent := present[key.binding]; isPresent {
				report.ReasonCode = capabilityMissingReason
			} else {
				report.ReasonCode = deviceMissingReason
			}
			reports = append(reports, report)
			continue
		}
		if available, exists := evidence[ieeeAddress]; exists {
			report.SourceObservedAt = available.receivedAt
			if available.available {
				report.Status = adapter.AvailabilityAvailable
			} else {
				report.Status = adapter.AvailabilityUnavailable
				report.ReasonCode = deviceOfflineReason
			}
			reports = append(reports, report)
		}
	}
	return z2m.reportAvailability(ctx, reports)
}

func (z2m *Adapter) processDeviceMessage(
	ctx context.Context,
	generation uint64,
	state *connectionSync,
	message mqttMessage,
) error {
	z2m.mutex.Lock()
	devices := z2m.devices
	z2m.mutex.Unlock()
	device, kind := classifyDeviceTopic(z2m.config.BaseTopic, message.Topic, devices)
	switch kind {
	case deviceTopicAvailability:
		if err := z2m.cacheAvailability(state, message, devices); err != nil {
			z2m.logger.WarnContext(
				ctx,
				"ignored invalid Zigbee2MQTT availability",
				"topic",
				message.Topic,
				"error",
				err,
			)
			return nil
		}
		evidence := state.availability[device.ieeeAddress]
		reports := make([]adapter.EntityAvailabilityReport, 0, len(device.entities))
		for _, entity := range device.entities {
			report := adapter.EntityAvailabilityReport{EntityID: entity.entityID, SourceObservedAt: evidence.receivedAt}
			if evidence.available {
				report.Status = adapter.AvailabilityAvailable
			} else {
				report.Status = adapter.AvailabilityUnavailable
				report.ReasonCode = deviceOfflineReason
			}
			reports = append(reports, report)
		}
		return z2m.reportAvailability(ctx, reports)
	case deviceTopicState:
		return z2m.publishDeviceState(ctx, generation, device, message)
	case deviceTopicUnknown:
		return nil
	}
	return nil
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

func (z2m *Adapter) clearAvailabilityEvidence(state *connectionSync) {
	clear(state.availability)
	kept := state.pendingOrder[:0]
	for _, topic := range state.pendingOrder {
		if strings.HasPrefix(topic, z2m.config.BaseTopic+"/") && strings.HasSuffix(topic, "/availability") {
			delete(state.pending, topic)
			continue
		}
		kept = append(kept, topic)
	}
	state.pendingOrder = kept
}

func (z2m *Adapter) cacheAvailability(
	state *connectionSync,
	message mqttMessage,
	devices map[string]runtimeDevice,
) error {
	device, kind := classifyDeviceTopic(z2m.config.BaseTopic, message.Topic, devices)
	if kind != deviceTopicAvailability {
		return errors.New("not an availability topic")
	}
	available, err := decodeAvailability(message.Payload)
	if err != nil {
		return err
	}
	state.availability[device.ieeeAddress] = availabilityEvidence{available: available, receivedAt: message.ReceivedAt}
	return nil
}

func (z2m *Adapter) publishDeviceState(
	ctx context.Context,
	generation uint64,
	device runtimeDevice,
	message mqttMessage,
) error {
	entities := make([]discoveredEntity, 0, len(device.entities))
	byKey := make(map[string]string, len(device.entities))
	for _, entity := range device.entities {
		entities = append(entities, entity.discovered)
		byKey[entity.discovered.Descriptor.Key] = entity.entityID
	}
	states, issues, err := decodeDeviceState(message.Payload, entities)
	if err != nil {
		z2m.logger.WarnContext(
			ctx,
			"ignored malformed Zigbee2MQTT Device State",
			"friendly_name",
			device.friendly,
			"error",
			err,
		)
		return nil
	}
	for _, issue := range issues {
		z2m.logger.WarnContext(
			ctx,
			"ignored invalid Zigbee2MQTT State property",
			"friendly_name",
			device.friendly,
			"property",
			issue.Property,
			"error",
			issue.Err,
		)
	}
	for _, state := range states {
		entityID := byKey[state.Entity.Descriptor.Key]
		if z2m.claimMatcher(generation, entityID, state, message.Retained, message.ReceivedAt) {
			continue
		}
		observation, observationErr := newObservation(entityID, state, message.ReceivedAt, nil)
		if observationErr != nil {
			return observationErr
		}
		if _, observationErr = z2m.session.PublishObservation(ctx, observation); observationErr != nil {
			return &sessionOperationError{operation: "publish Zigbee2MQTT Observation", err: observationErr}
		}
	}
	return nil
}

func newObservation(
	entityID string,
	state decodedEntityState,
	receivedAt time.Time,
	refreshForCommand *string,
) (adapter.Observation, error) {
	switch state.Entity.Kind {
	case entityKindPower:
		return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
			EntityID: entityID, Support: powerSupport(), State: contractpowerv1.State(state.Power),
			AdapterReceivedAt: receivedAt, RefreshForCommand: refreshForCommand,
		})
	case entityKindBrightness:
		return sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
			EntityID: entityID, Support: brightnessSupport(), State: contractbrightnessv1.State(state.Brightness),
			AdapterReceivedAt: receivedAt, RefreshForCommand: refreshForCommand,
		})
	default:
		return adapter.Observation{}, errors.New("unknown Zigbee2MQTT Entity kind")
	}
}

func powerSupport() contractpowerv1.Support {
	return contractpowerv1.Support{
		State:      contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}},
	}
}

func brightnessSupport() contractbrightnessv1.Support {
	return contractbrightnessv1.Support{
		State:      contractbrightnessv1.StateSupport{Maximum: hearthBrightnessMaximum},
		Operations: contractbrightnessv1.OperationSupport{Set: contractbrightnessv1.SetSupport{Step: 1}},
	}
}

func (z2m *Adapter) reportAvailability(ctx context.Context, reports []adapter.EntityAvailabilityReport) error {
	for len(reports) > 0 {
		count := min(len(reports), availabilityPage)
		if err := z2m.session.ReportEntityAvailability(ctx, reports[:count]); err != nil {
			return &sessionOperationError{operation: "report Zigbee2MQTT Entity availability", err: err}
		}
		reports = reports[count:]
	}
	return nil
}

func publishGet(ctx context.Context, connection mqttConnection, base, friendly, property string) error {
	payload, err := json.Marshal(map[string]string{property: ""})
	if err != nil {
		return err
	}
	return connection.Publish(ctx, base+"/"+friendly+"/get", mqttQoS, false, payload)
}

func (z2m *Adapter) reportUnhealthy(ctx context.Context, reason string) error {
	z2m.disableRoutes()
	if err := z2m.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthUnhealthy, SourceObservedAt: time.Now().UTC(), ReasonCode: reason,
	}); err != nil {
		return &sessionOperationError{operation: "report unhealthy Zigbee2MQTT bridge", err: err}
	}
	return nil
}

func (z2m *Adapter) setConnection(
	generation uint64,
	connection mqttConnection,
	cancel context.CancelCauseFunc,
) {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation == generation {
		z2m.connection = connection
		z2m.connectionCancel = cancel
	}
}

func (z2m *Adapter) clearConnection(generation uint64) {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation != generation {
		return
	}
	z2m.connection = nil
	z2m.connectionCancel = nil
	z2m.healthy = false
	z2m.routes = make(map[string]commandRoute)
	z2m.devices = make(map[string]runtimeDevice)
	z2m.cancelMatchersLocked()
}

func (z2m *Adapter) disableRoutes() {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	z2m.healthy = false
	z2m.routes = make(map[string]commandRoute)
	z2m.devices = make(map[string]runtimeDevice)
	z2m.cancelMatchersLocked()
}

func (z2m *Adapter) installSnapshot(generation uint64, snapshot routeSnapshot) {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.generation != generation {
		return
	}
	z2m.routeSerial++
	for entityID, route := range snapshot.routes {
		route.routeGeneration = z2m.routeSerial
		snapshot.routes[entityID] = route
	}
	z2m.cancelMatchersLocked()
	z2m.routes = snapshot.routes
	z2m.devices = snapshot.devices
	z2m.healthy = true
}

func (z2m *Adapter) cancelMatchersLocked() {
	for entityID, matcher := range z2m.matchers {
		delete(z2m.matchers, entityID)
		close(matcher.canceled)
	}
}

func (z2m *Adapter) isHealthy(generation uint64) bool {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	return z2m.generation == generation && z2m.healthy
}

//nolint:gosec // Backoff jitter needs no cryptographic randomness.
func jitterReconnect(delay time.Duration) time.Duration {
	half := delay / jitterDivisor
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

type bridgeTopic uint8

const (
	bridgeTopicUnknown bridgeTopic = iota
	bridgeTopicInfo
	bridgeTopicState
	bridgeTopicDevices
	bridgeTopicEvent
)

func classifyBridgeTopic(base, topic string) bridgeTopic {
	switch topic {
	case base + "/bridge/info":
		return bridgeTopicInfo
	case base + "/bridge/state":
		return bridgeTopicState
	case base + "/bridge/devices":
		return bridgeTopicDevices
	case base + "/bridge/event":
		return bridgeTopicEvent
	default:
		return bridgeTopicUnknown
	}
}

type deviceTopic uint8

const (
	deviceTopicUnknown deviceTopic = iota
	deviceTopicState
	deviceTopicAvailability
)

func parseDeviceTopic(base, topic string) (string, deviceTopic) {
	remainder, ok := strings.CutPrefix(topic, base+"/")
	if !ok {
		return "", deviceTopicUnknown
	}
	if validRouteSlug(remainder) && remainder != "bridge" {
		return remainder, deviceTopicState
	}
	friendly, suffix, hasSuffix := strings.Cut(remainder, "/")
	if hasSuffix && suffix == "availability" && validRouteSlug(friendly) && friendly != "bridge" {
		return friendly, deviceTopicAvailability
	}
	return "", deviceTopicUnknown
}

func queueableDeviceTopic(base, topic string, inventory *inventoryDiscovery) bool {
	friendly, kind := parseDeviceTopic(base, topic)
	if kind == deviceTopicUnknown {
		return false
	}
	if inventory == nil {
		return true
	}
	for _, device := range inventory.Devices {
		if device.FriendlyName == friendly {
			return true
		}
	}
	return false
}

func classifyDeviceTopic(base, topic string, devices map[string]runtimeDevice) (runtimeDevice, deviceTopic) {
	friendly, kind := parseDeviceTopic(base, topic)
	device, exists := devices[friendly]
	if !exists {
		return runtimeDevice{}, deviceTopicUnknown
	}
	return device, kind
}
