package scripted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// availabilityBatchLimit mirrors the SDK's maximum reports per
// ReportEntityAvailability call. A config may register 64 Entities on each of
// any number of Devices, so Initialize pages availability reports at this size.
const availabilityBatchLimit = 256

// aggregateDeviceHealth folds per-Device health into one process-level
// report: the first unhealthy Device wins, and a fully healthy config reports
// healthy once. It also returns the Binding keys whose Entities must not
// receive an availability report: a Device configured to omit them while it
// reports unhealthy leaves its Entities to Core as effectively unavailable.
func aggregateDeviceHealth(
	devices []scriptedDevice,
) (adapter.HealthReport, map[string]struct{}, error) {
	health := adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC()}
	omitAvailability := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		healthy, reason, err := device.spec.HealthStatus()
		if err != nil {
			return adapter.HealthReport{}, nil, err
		}
		if !healthy && health.Status == adapter.HealthHealthy {
			health.Status = adapter.HealthUnhealthy
			health.ReasonCode = reason
		}
		if !healthy && device.spec.OmitAvailabilityWhenUnhealthy {
			omitAvailability[device.spec.BindingKey] = struct{}{}
		}
	}
	return health, omitAvailability, nil
}

// buildAvailabilityReports reports every Entity available unless its own
// config marks it unavailable, skipping the Binding keys in omit.
func buildAvailabilityReports(
	ordered []*scriptedEntity,
	omit map[string]struct{},
) []adapter.EntityAvailabilityReport {
	reports := make([]adapter.EntityAvailabilityReport, 0, len(ordered))
	for _, entity := range ordered {
		if _, skip := omit[entity.bindingKey]; skip {
			continue
		}
		report := adapter.EntityAvailabilityReport{
			EntityID:         entity.entityID,
			Status:           adapter.AvailabilityAvailable,
			SourceObservedAt: time.Now().UTC(),
		}
		if !entity.available {
			report.Status = adapter.AvailabilityUnavailable
			report.ReasonCode = entity.unavailabilityReason
		}
		reports = append(reports, report)
	}
	return reports
}

// Session is the narrow SDK seam the scripted runtime needs. It mirrors the
// surface the scenario simulator uses so both runtimes stay substitutable.
type Session interface {
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
	PublishEntityEvent(context.Context, adapter.EntityEvent) (adapter.EntityEventID, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
}

// UnknownEntityError reports a control or Command reference to an Entity ID
// the runtime never registered. Control callers map it to 404.
type UnknownEntityError struct {
	EntityID string
}

func (err *UnknownEntityError) Error() string {
	return fmt.Sprintf("no scripted Entity %q", err.EntityID)
}

// EntityInfo is the control-channel snapshot of one scripted Entity.
type EntityInfo struct {
	BindingKey  string          `json:"binding_key"`
	Key         string          `json:"key"`
	EntityID    string          `json:"entity_id"`
	EntityType  string          `json:"entity_type"`
	EventSource bool            `json:"event_source"`
	Paused      bool            `json:"paused"`
	Current     json.RawMessage `json:"current"`
}

// PublicationResult carries the canonical publication IDs the Session issued
// for one control publish. Exactly one field is set on success: ObservationID
// for a State Entity, EventID for an event source. A failed publish returns the
// zero value, so an ID never appears for a report that was not stored even when
// the Session minted one before failing.
type PublicationResult struct {
	ObservationID adapter.ObservationID `json:"observation_id,omitempty"`
	EventID       adapter.EntityEventID `json:"event_id,omitempty"`
}

type scriptedEntity struct {
	bindingKey string
	key        string
	name       string
	entityID   string
	codecs     *TypeCodecs
	support    json.RawMessage
	current    json.RawMessage
	script     []json.RawMessage
	index      int
	interval   time.Duration
	paused     bool
	commands   map[string]CommandBehavior
	eventNames map[string]struct{}
	available  bool
	// unavailabilityReason carries the configured reason code for Entities
	// that start unavailable. Core requires a reason, and config validation
	// rejects a missing one, so Initialize can report it directly.
	unavailabilityReason string
	// sourceTimeOffset and receivedTimeOffset shift each Observation's
	// SourceUpdatedAt and AdapterReceivedAt to now plus the offset. A zero
	// source offset leaves SourceUpdatedAt unset, as when it is not configured.
	sourceTimeOffset   time.Duration
	receivedTimeOffset time.Duration
}

type scriptedDevice struct {
	spec     DeviceSpec
	external string
}

// Runtime owns every scripted Entity for one adapter process. Command handler
// invocations may overlap, so all Entity state is guarded by one mutex.
type Runtime struct {
	session Session
	mutex   sync.Mutex
	devices []scriptedDevice
	byID    map[string]*scriptedEntity
	ordered []*scriptedEntity
}

// New validates the device specs, normalizes every configured value against
// its Entity type's authoritative schemas, and returns a runtime that is ready
// to register. The first invalid value fails with its Device, Entity, and
// value index so agents can fix config without reading Go.
func New(session Session, devices []DeviceSpec) (*Runtime, error) {
	if session == nil {
		return nil, fmt.Errorf("scripted runtime Session is required")
	}
	compiled, err := compile(devices)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		session: session,
		byID:    make(map[string]*scriptedEntity),
		devices: compiled.devices,
		ordered: compiled.ordered,
	}, nil
}

type compiledConfig struct {
	devices []scriptedDevice
	ordered []*scriptedEntity
}

// ValidateValues fully validates device specs and normalizes every
// configured value against its Entity type's authoritative schemas without a
// Session, so config files (including checked-in examples) get complete value
// checking in tests and in load paths that never connect to NATS.
func ValidateValues(devices []DeviceSpec) error {
	_, err := compile(devices)
	return err
}

func compile(devices []DeviceSpec) (compiledConfig, error) {
	if len(devices) == 0 {
		return compiledConfig{}, fmt.Errorf("at least one scripted Device is required")
	}
	var compiled compiledConfig
	seenBindings := make(map[string]struct{}, len(devices))
	for deviceIndex := range devices {
		spec := &devices[deviceIndex]
		if err := spec.Validate(); err != nil {
			return compiledConfig{}, fmt.Errorf("devices[%d]: %w", deviceIndex, err)
		}
		if _, duplicate := seenBindings[spec.BindingKey]; duplicate {
			return compiledConfig{}, fmt.Errorf("devices[%d]: duplicate binding_key %q", deviceIndex, spec.BindingKey)
		}
		seenBindings[spec.BindingKey] = struct{}{}
		device := scriptedDevice{spec: *spec, external: spec.BindingKey}
		for entityIndex := range spec.Entities {
			entitySpec := &spec.Entities[entityIndex]
			entity, err := buildEntity(spec.BindingKey, entitySpec)
			if err != nil {
				return compiledConfig{}, fmt.Errorf("devices[%d] entities[%d]: %w", deviceIndex, entityIndex, err)
			}
			compiled.ordered = append(compiled.ordered, entity)
		}
		compiled.devices = append(compiled.devices, device)
	}
	return compiled, nil
}

func buildEntity(bindingKey string, spec *EntitySpec) (*scriptedEntity, error) {
	codecs, lookupErr := Lookup(spec.Type)
	if lookupErr != nil {
		return nil, lookupErr
	}
	supportRaw, supportErr := toJSON(spec.Support)
	if supportErr != nil {
		return nil, fmt.Errorf("invalid support: %w", supportErr)
	}
	support, normalizeErr := codecs.NormalizeSupport(supportRaw)
	if normalizeErr != nil {
		return nil, fmt.Errorf("invalid support: %w", normalizeErr)
	}
	entity := &scriptedEntity{
		bindingKey:           bindingKey,
		key:                  spec.Key,
		name:                 spec.Name,
		codecs:               codecs,
		support:              support,
		commands:             spec.Commands,
		available:            spec.Available == nil || *spec.Available,
		unavailabilityReason: spec.AvailabilityReason,
		sourceTimeOffset:     time.Duration(spec.SourceTimeOffset),
		receivedTimeOffset:   time.Duration(spec.ReceivedTimeOffset),
	}
	if err := initEntityValue(entity, spec); err != nil {
		return nil, err
	}
	if spec.Outputs != nil {
		entity.interval = time.Duration(spec.Outputs.Interval)
		for valueIndex, value := range spec.Outputs.Values {
			normalized, scriptErr := normalizeScriptValue(codecs, entity.eventNames, value)
			if scriptErr != nil {
				return nil, fmt.Errorf("outputs values[%d]: %w", valueIndex, scriptErr)
			}
			entity.script = append(entity.script, normalized)
		}
	}
	return entity, nil
}

// initEntityValue sets the starting value: the supported event names for
// event-source Entities, or the normalized initial State otherwise.
func initEntityValue(entity *scriptedEntity, spec *EntitySpec) error {
	if entity.codecs.EventSource {
		if spec.Initial != nil {
			return fmt.Errorf("event-source Entities take no initial value")
		}
		names, err := supportedEventNames(entity.support)
		if err != nil {
			return err
		}
		entity.eventNames = names
		return nil
	}
	initialRaw, err := toJSON(spec.Initial)
	if err != nil {
		return fmt.Errorf("initial value is required: %w", err)
	}
	initial, err := entity.codecs.NormalizeState(initialRaw)
	if err != nil {
		return fmt.Errorf("invalid initial value: %w", err)
	}
	entity.current = initial
	return nil
}

func normalizeScriptValue(
	codecs *TypeCodecs,
	eventNames map[string]struct{},
	value any,
) (json.RawMessage, error) {
	raw, valueErr := toJSON(value)
	if valueErr != nil {
		return nil, valueErr
	}
	if codecs.EventSource {
		var name string
		if decodeErr := json.Unmarshal(raw, &name); decodeErr != nil || name == "" {
			return nil, fmt.Errorf("event name must be a non-empty string")
		}
		if _, ok := eventNames[name]; !ok {
			return nil, fmt.Errorf("event name %q is not in the Entity support", name)
		}
		return json.RawMessage(fmt.Sprintf("%q", name)), nil
	}
	normalized, stateErr := codecs.NormalizeState(raw)
	if stateErr != nil {
		return nil, stateErr
	}
	return normalized, nil
}

func supportedEventNames(support json.RawMessage) (map[string]struct{}, error) {
	var decoded struct {
		Events struct {
			Names []string `json:"names"`
		} `json:"events"`
	}
	if err := json.Unmarshal(support, &decoded); err != nil {
		return nil, fmt.Errorf("decode supported event names: %w", err)
	}
	if len(decoded.Events.Names) == 0 {
		return nil, fmt.Errorf("event-source support lists no event names")
	}
	names := make(map[string]struct{}, len(decoded.Events.Names))
	for _, name := range decoded.Events.Names {
		names[name] = struct{}{}
	}
	return names, nil
}

// Registrations returns one SDK registration per scripted Device.
func (runtime *Runtime) Registrations() []adapter.Registration {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	registrations := make([]adapter.Registration, 0, len(runtime.devices))
	for _, device := range runtime.devices {
		registration := adapter.Registration{
			BindingKey: device.spec.BindingKey,
			Device: adapter.DeviceDescriptor{
				ExternalID: &device.external,
				Name:       device.spec.Name,
				Kind:       device.spec.Kind,
			},
		}
		for _, entity := range runtime.ordered {
			if entity.bindingKey != device.spec.BindingKey {
				continue
			}
			registration.Entities = append(registration.Entities, adapter.EntityDescriptor{
				Key:        entity.key,
				ExternalID: device.spec.BindingKey + "." + entity.key,
				Name:       entity.name,
				Type:       entity.codecs.TypeID,
				Support:    entity.support,
			})
		}
		registrations = append(registrations, registration)
	}
	return registrations
}

// Attach binds canonical Entity IDs from registration responses. It must run
// before Initialize, CommandHandler, or any script step.
func (runtime *Runtime) Attach(bindings []adapter.Binding) error {
	byKey := make(map[string]map[string]string, len(bindings))
	for _, binding := range bindings {
		entities := make(map[string]string, len(binding.Entities))
		for _, entity := range binding.Entities {
			entities[entity.Key] = entity.EntityID
		}
		byKey[binding.BindingKey] = entities
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	for _, entity := range runtime.ordered {
		entities, ok := byKey[entity.bindingKey]
		if !ok {
			return fmt.Errorf("registration response omitted Binding %q", entity.bindingKey)
		}
		id, ok := entities[entity.key]
		if !ok || id == "" {
			return fmt.Errorf("registration response omitted Entity key %q", entity.key)
		}
		if existing, duplicate := runtime.byID[id]; duplicate && existing != entity {
			return fmt.Errorf("registration response reused Entity ID %q", id)
		}
		entity.entityID = id
		runtime.byID[id] = entity
	}
	return nil
}

// Initialize reports per-Device health and per-Entity availability, then
// publishes the first value of every Entity: script values[0] when a script
// exists, otherwise the configured initial value.
func (runtime *Runtime) Initialize(ctx context.Context) error {
	runtime.mutex.Lock()
	ordered := append([]*scriptedEntity(nil), runtime.ordered...)
	devices := append([]scriptedDevice(nil), runtime.devices...)
	runtime.mutex.Unlock()
	health, omitAvailability, healthErr := aggregateDeviceHealth(devices)
	if healthErr != nil {
		return healthErr
	}
	if setErr := runtime.session.SetHealth(ctx, health); setErr != nil {
		return fmt.Errorf("report Adapter health: %w", setErr)
	}
	reports := buildAvailabilityReports(ordered, omitAvailability)
	for start := 0; start < len(reports); start += availabilityBatchLimit {
		end := min(start+availabilityBatchLimit, len(reports))
		if reportErr := runtime.session.ReportEntityAvailability(ctx, reports[start:end]); reportErr != nil {
			// Core rejects availability batches from an unhealthy Adapter by
			// design. An Adapter that just reported itself unhealthy keeps its
			// Entities effectively unavailable through adapter health, so that
			// rejection is not a startup failure. Every other report error
			// still is.
			var rejected *adapter.EntityAvailabilityRejectedError
			if health.Status == adapter.HealthUnhealthy &&
				errors.As(reportErr, &rejected) &&
				rejected.Code == adapter.EntityAvailabilityAdapterUnhealthy {
				continue
			}
			return fmt.Errorf("report Entity availability: %w", reportErr)
		}
	}
	for _, entity := range ordered {
		// An Entity marked available:false has no known State, so publishing its
		// configured initial value would materialize a State row for unknown
		// State. It stays silent until a Command or control publish repairs it;
		// nil or true still publishes exactly once.
		if !entity.available {
			continue
		}
		if firstErr := runtime.publishFirst(ctx, entity); firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func (runtime *Runtime) publishFirst(ctx context.Context, entity *scriptedEntity) error {
	runtime.mutex.Lock()
	if len(entity.script) > 0 {
		entity.current = entity.script[0]
		entity.index = 0
	}
	current := entity.current
	runtime.mutex.Unlock()
	if entity.codecs.EventSource {
		if len(current) == 0 {
			return nil
		}
		_, publishErr := runtime.publishEvent(ctx, entity, eventNameOf(current))
		return publishErr
	}
	if len(current) == 0 {
		return nil
	}
	_, publishErr := runtime.session.PublishObservation(ctx, newObservation(entity, current))
	return publishErr
}

func newObservation(entity *scriptedEntity, value json.RawMessage) adapter.Observation {
	now := time.Now().UTC()
	observation := adapter.Observation{
		EntityID:          entity.entityID,
		Value:             value,
		AdapterReceivedAt: now.Add(entity.receivedTimeOffset).Format(time.RFC3339Nano),
	}
	if entity.sourceTimeOffset != 0 {
		sourceUpdatedAt := now.Add(entity.sourceTimeOffset).Format(time.RFC3339Nano)
		observation.SourceUpdatedAt = &sourceUpdatedAt
	}
	return observation
}

func eventNameOf(raw json.RawMessage) string {
	var name string
	_ = json.Unmarshal(raw, &name)
	return name
}

// requireSupportedEventName fails when name is empty or absent from the
// Entity's supported event names. It never mutates Entity state, so callers can
// validate a requested name before recording anything.
func (runtime *Runtime) requireSupportedEventName(entity *scriptedEntity, name string) error {
	if name == "" {
		return fmt.Errorf("event name is required")
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if _, supported := entity.eventNames[name]; !supported {
		return fmt.Errorf("event name %q is not in the Entity support", name)
	}
	return nil
}

// publishEvent reports one supported Entity Event and returns the canonical
// event ID the Session issued. Callers that only need the report to happen —
// the script ticker and Initialize — discard the ID.
func (runtime *Runtime) publishEvent(
	ctx context.Context,
	entity *scriptedEntity,
	name string,
) (adapter.EntityEventID, error) {
	if err := runtime.requireSupportedEventName(entity, name); err != nil {
		return "", err
	}
	eventID, err := runtime.session.PublishEntityEvent(ctx, adapter.EntityEvent{
		EntityID: entity.entityID,
		Name:     name,
	})
	if err != nil {
		return "", err
	}
	return eventID, nil
}

func (runtime *Runtime) entityByID(entityID string) (*scriptedEntity, error) {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	entity, ok := runtime.byID[entityID]
	if !ok {
		return nil, &UnknownEntityError{EntityID: entityID}
	}
	return entity, nil
}

// CommandHandler returns one multiplexed handler covering every scripted
// Entity. Operations without configured behavior accept, apply parameters, and
// publish the value those parameters produced, captured before Accept so a
// concurrent writer cannot change what the Command reports.
func (runtime *Runtime) CommandHandler() adapter.CommandHandler {
	return func(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
		entity, err := runtime.entityByID(command.EntityID)
		if err != nil {
			return err
		}
		behavior := entity.commands[command.OperationName]
		if behaviorName(behavior) == CommandBehaviorReject {
			return responder.Reject(rejectReason(behavior))
		}
		// mark_available repairs an Entity that started unavailable: report
		// available before applying parameters or accepting, and surface a
		// failed re-report as the Command's error.
		if behavior.MarkAvailable {
			if reportErr := runtime.session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{{
				EntityID:         entity.entityID,
				Status:           adapter.AvailabilityAvailable,
				SourceObservedAt: time.Now().UTC(),
			}}); reportErr != nil {
				return reportErr
			}
		}
		switch behaviorName(behavior) {
		case CommandBehaviorAcceptSilent:
			_, acceptErr := responder.Accept()
			return acceptErr
		default:
			current := runtime.commandPublishState(entity, behavior, command.Parameters)
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			if entity.codecs.EventSource {
				return nil
			}
			_, err = evidence.PublishObservation(ctx, newObservation(entity, current))
			return err
		}
	}
}

func behaviorName(behavior CommandBehavior) string {
	if behavior.Behavior == "" {
		return CommandBehaviorAcceptPublish
	}
	return behavior.Behavior
}

func rejectReason(behavior CommandBehavior) string {
	if behavior.Reason != "" {
		return behavior.Reason
	}
	return "simulated rejection"
}

func applyParameters(behavior CommandBehavior) bool {
	return behavior.ApplyParameters == nil || *behavior.ApplyParameters
}

// commandPublishState applies the Command's parameter override when its
// behavior allows it and returns the State this Command publishes. The
// override and the capture share one lock acquisition, so a concurrent Tick or
// Command cannot replace Entity state while the responder Accept blocks on I/O.
func (runtime *Runtime) commandPublishState(
	entity *scriptedEntity,
	behavior CommandBehavior,
	parameters json.RawMessage,
) json.RawMessage {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	if applyParameters(behavior) {
		applyCommandParametersLocked(entity, parameters)
	}
	return entity.current
}

// applyCommandParametersLocked implements the documented generic rule:
// parameters shaped {"value": X} replace a scalar State, or the "value" member
// of an object State. Anything else leaves State unchanged. Values that fail
// schema validation keep the previous State; Core remains the enforcing
// validator. The caller must hold runtime.mutex.
func applyCommandParametersLocked(entity *scriptedEntity, parameters json.RawMessage) {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(parameters, &decoded); err != nil {
		return
	}
	value, ok := decoded["value"]
	if !ok {
		return
	}
	var current any
	if decodeErr := json.Unmarshal(entity.current, &current); decodeErr != nil {
		return
	}
	var candidate json.RawMessage
	if currentMap, isObject := current.(map[string]any); isObject {
		if _, hasValue := currentMap["value"]; !hasValue {
			return
		}
		merged := make(map[string]any, len(currentMap))
		maps.Copy(merged, currentMap)
		var valueAny any
		if valueErr := json.Unmarshal(value, &valueAny); valueErr != nil {
			return
		}
		merged["value"] = valueAny
		raw, marshalErr := json.Marshal(merged)
		if marshalErr != nil {
			return
		}
		candidate = raw
	} else {
		candidate = value
	}
	if normalized, normalizeErr := entity.codecs.NormalizeState(candidate); normalizeErr == nil {
		entity.current = normalized
	}
}

// Tick advances one Entity's script by one entry and publishes it. It is the
// single step the interval loop and the control channel share, so tests drive
// it directly for deterministic assertions.
func (runtime *Runtime) Tick(ctx context.Context, entityID string) error {
	entity, err := runtime.entityByID(entityID)
	if err != nil {
		return err
	}
	runtime.mutex.Lock()
	if entity.paused || len(entity.script) == 0 {
		runtime.mutex.Unlock()
		return nil
	}
	entity.index = (entity.index + 1) % len(entity.script)
	entity.current = entity.script[entity.index]
	current := entity.current
	runtime.mutex.Unlock()
	if entity.codecs.EventSource {
		_, publishErr := runtime.publishEvent(ctx, entity, eventNameOf(current))
		return publishErr
	}
	_, publishErr := runtime.session.PublishObservation(ctx, newObservation(entity, current))
	return publishErr
}

// PublishNow publishes one value immediately and makes it current: a State for
// State Entities, or {"name": "<event>"} for event sources. A nil value
// republishes the current State. On success it returns the canonical ID the
// Session issued for this publication: the Observation ID for a State Entity or
// the Entity Event ID for an event source.
func (runtime *Runtime) PublishNow(
	ctx context.Context,
	entityID string,
	value json.RawMessage,
) (PublicationResult, error) {
	entity, err := runtime.entityByID(entityID)
	if err != nil {
		return PublicationResult{}, err
	}
	if entity.codecs.EventSource {
		var body struct {
			Name string `json:"name"`
		}
		if decodeErr := json.Unmarshal(value, &body); decodeErr != nil || body.Name == "" {
			return PublicationResult{}, fmt.Errorf("event publish requires {\"name\": \"<event>\"}")
		}
		// Validate before mutating: an unsupported name must fail without
		// leaving itself as the snapshot's current value.
		if supportErr := runtime.requireSupportedEventName(entity, body.Name); supportErr != nil {
			return PublicationResult{}, supportErr
		}
		runtime.mutex.Lock()
		entity.current = json.RawMessage(fmt.Sprintf("%q", body.Name))
		runtime.mutex.Unlock()
		eventID, publishErr := runtime.publishEvent(ctx, entity, body.Name)
		if publishErr != nil {
			return PublicationResult{}, publishErr
		}
		return PublicationResult{EventID: eventID}, nil
	}
	runtime.mutex.Lock()
	current := entity.current
	runtime.mutex.Unlock()
	if len(value) > 0 {
		normalized, normalizeErr := entity.codecs.NormalizeState(value)
		if normalizeErr != nil {
			return PublicationResult{}, fmt.Errorf("invalid State: %w", normalizeErr)
		}
		runtime.mutex.Lock()
		entity.current = normalized
		current = normalized
		runtime.mutex.Unlock()
	}
	if len(current) == 0 {
		return PublicationResult{}, fmt.Errorf("entity has no current State to publish")
	}
	observationID, publishErr := runtime.session.PublishObservation(ctx, newObservation(entity, current))
	if publishErr != nil {
		return PublicationResult{}, publishErr
	}
	return PublicationResult{ObservationID: observationID}, nil
}

// PublishEnvelope publishes one control-channel body shaped
// {"value": <json>} for State Entities or {"name": "<event>"} for event
// sources. A State body without a value republishes the current State. On
// success it returns the canonical ID the Session issued for that publication.
func (runtime *Runtime) PublishEnvelope(
	ctx context.Context,
	entityID string,
	body json.RawMessage,
) (PublicationResult, error) {
	entity, err := runtime.entityByID(entityID)
	if err != nil {
		return PublicationResult{}, err
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
		Name  string          `json:"name"`
	}
	if decodeErr := json.Unmarshal(body, &envelope); decodeErr != nil {
		return PublicationResult{}, fmt.Errorf("invalid publish body: %w", decodeErr)
	}
	if entity.codecs.EventSource {
		return runtime.PublishNow(ctx, entityID, json.RawMessage(fmt.Sprintf(`{"name":%q}`, envelope.Name)))
	}
	return runtime.PublishNow(ctx, entityID, envelope.Value)
}

// SetPaused holds or resumes one Entity's script ticker. Commands and control
// publishes keep working while paused.
func (runtime *Runtime) SetPaused(entityID string, paused bool) error {
	entity, err := runtime.entityByID(entityID)
	if err != nil {
		return err
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	entity.paused = paused
	return nil
}

// SnapshotFor returns the control-channel snapshot of one Entity.
func (runtime *Runtime) SnapshotFor(entityID string) (EntityInfo, error) {
	entity, err := runtime.entityByID(entityID)
	if err != nil {
		return EntityInfo{}, err
	}
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	return entity.info(), nil
}

// Snapshot lists every scripted Entity with its current value for the control
// channel.
func (entity *scriptedEntity) info() EntityInfo {
	return EntityInfo{
		BindingKey:  entity.bindingKey,
		Key:         entity.key,
		EntityID:    entity.entityID,
		EntityType:  entity.codecs.TypeID,
		EventSource: entity.codecs.EventSource,
		Paused:      entity.paused,
		Current:     entity.current,
	}
}

func (runtime *Runtime) Snapshot() []EntityInfo {
	runtime.mutex.Lock()
	defer runtime.mutex.Unlock()
	infos := make([]EntityInfo, 0, len(runtime.ordered))
	for _, entity := range runtime.ordered {
		infos = append(infos, entity.info())
	}
	return infos
}

// StartScripts runs one ticker per Entity with a multi-value script until ctx
// ends. It returns a join function the caller defers before closing the
// Session. Single-value and valueless Entities publish once at Initialize and
// need no goroutine.
func (runtime *Runtime) StartScripts(
	ctx context.Context,
	logger *slog.Logger,
) func() {
	if logger == nil {
		logger = slog.Default()
	}
	runtime.mutex.Lock()
	var entities []*scriptedEntity
	for _, entity := range runtime.ordered {
		if len(entity.script) > 1 && entity.interval > 0 {
			entities = append(entities, entity)
		}
	}
	runtime.mutex.Unlock()
	var workers sync.WaitGroup
	for _, entity := range entities {
		workers.Go(func() {
			ticker := time.NewTicker(entity.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := runtime.Tick(ctx, entity.entityID); err != nil {
						logger.WarnContext(ctx, "scripted tick failed",
							slog.String("component", "simulator"),
							slog.String("event", "simulator.script_tick_failed"),
							slog.String("entity_id", entity.entityID),
							slog.String("error_code", "script_tick_failed"),
						)
					}
				}
			}
		})
	}
	return workers.Wait
}
