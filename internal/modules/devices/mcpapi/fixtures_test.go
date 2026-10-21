package mcpapi //nolint:testpackage // Tests use private transport helpers and domain doubles.

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	apiAdapterID     = "simulator"
	apiEntityID      = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiDeviceID      = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiObservationID = devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type stubDevices struct {
	getEntity                     func(context.Context, devices.EntityID) (devices.EntityWithState, error)
	setEntityEnabled              func(context.Context, devices.EntityID, bool) (devices.EntityWithState, error)
	listDevices                   func(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	getDevice                     func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	listEntities                  func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	getCommand                    func(context.Context, devices.CommandID) (devices.CommandRecord, error)
	listEntityCommands            func(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
	listCommands                  func(context.Context, devices.ListCommandsParams) (devices.Page[devices.CommandRecord], error)
	listEntityAvailabilityHistory func(
		context.Context,
		devices.ListEntityAvailabilityParams,
	) (devices.Page[devices.HealthTransition], error)
	listEntityStateHistory func(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error)
	listEntityEvents func(
		context.Context,
		devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error)
	executeCommand func(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error)
}

func (stub *stubDevices) GetEntity(ctx context.Context, entityID devices.EntityID) (devices.EntityWithState, error) {
	if stub.getEntity == nil {
		panic("unexpected GetEntity call")
	}
	return stub.getEntity(ctx, entityID)
}

func (stub *stubDevices) SetEntityEnabled(
	ctx context.Context,
	entityID devices.EntityID,
	enabled bool,
) (devices.EntityWithState, error) {
	if stub.setEntityEnabled == nil {
		panic("unexpected SetEntityEnabled call")
	}
	return stub.setEntityEnabled(ctx, entityID, enabled)
}

func (stub *stubDevices) ListDevices(
	ctx context.Context,
	params devices.ListDevicesParams,
) (devices.Page[devices.Device], error) {
	if stub.listDevices == nil {
		panic("unexpected ListDevices call")
	}
	return stub.listDevices(ctx, params)
}

func (stub *stubDevices) GetDevice(
	ctx context.Context,
	params devices.GetDeviceParams,
) (devices.DeviceAggregate, error) {
	if stub.getDevice == nil {
		panic("unexpected GetDevice call")
	}
	return stub.getDevice(ctx, params)
}

func (stub *stubDevices) ListEntities(
	ctx context.Context,
	params devices.ListEntitiesParams,
) (devices.Page[devices.EntityWithState], error) {
	if stub.listEntities == nil {
		panic("unexpected ListEntities call")
	}
	return stub.listEntities(ctx, params)
}

func (stub *stubDevices) GetCommand(ctx context.Context, id devices.CommandID) (devices.CommandRecord, error) {
	if stub.getCommand == nil {
		panic("unexpected GetCommand call")
	}
	return stub.getCommand(ctx, id)
}

func (stub *stubDevices) ListEntityCommands(
	ctx context.Context,
	params devices.ListEntityCommandsParams,
) (devices.Page[devices.CommandRecord], error) {
	if stub.listEntityCommands == nil {
		panic("unexpected ListEntityCommands call")
	}
	return stub.listEntityCommands(ctx, params)
}

func (stub *stubDevices) ListCommands(
	ctx context.Context,
	params devices.ListCommandsParams,
) (devices.Page[devices.CommandRecord], error) {
	if stub.listCommands == nil {
		panic("unexpected ListCommands call")
	}
	return stub.listCommands(ctx, params)
}

func (stub *stubDevices) ListEntityAvailabilityHistory(
	ctx context.Context,
	params devices.ListEntityAvailabilityParams,
) (devices.Page[devices.HealthTransition], error) {
	if stub.listEntityAvailabilityHistory == nil {
		panic("unexpected ListEntityAvailabilityHistory call")
	}
	return stub.listEntityAvailabilityHistory(ctx, params)
}

func (stub *stubDevices) ListEntityStateHistory(
	ctx context.Context,
	params devices.ListEntityStateHistoryParams,
) (devices.Page[devices.EntityStateHistoryEntry], error) {
	if stub.listEntityStateHistory == nil {
		panic("unexpected ListEntityStateHistory call")
	}
	return stub.listEntityStateHistory(ctx, params)
}

func (stub *stubDevices) ListEntityEvents(
	ctx context.Context,
	params devices.ListEntityEventsParams,
) (devices.Page[devices.EntityEventHistoryEntry], error) {
	if stub.listEntityEvents == nil {
		panic("unexpected ListEntityEvents call")
	}
	return stub.listEntityEvents(ctx, params)
}

func (stub *stubDevices) ExecuteCommand(
	ctx context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	if stub.executeCommand == nil {
		panic("unexpected ExecuteCommand call")
	}
	return stub.executeCommand(ctx, input)
}

func apiEntityWithState(state *devices.State) devices.EntityWithState {
	evidenceAt := time.Date(2026, 8, 22, 12, 0, 1, 0, time.UTC)
	sourceObservedAt := evidenceAt.Add(-time.Second)
	return devices.EntityWithState{
		Entity: devices.Entity{
			ID:        apiEntityID,
			DeviceID:  apiDeviceID,
			AdapterID: "simulator",
			Name:      "Power",
			TypeID:    devices.EntityTypePowerV1,
			Support:   devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			Enabled:   true,
		},
		State: state,
		Availability: devices.EntityAvailability{
			Status: devices.EntityAvailabilityAvailable, Source: "entity_report",
			Since: evidenceAt, EvidenceAt: evidenceAt, SourceObservedAt: &sourceObservedAt,
		},
	}
}

const apiCommandID = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")

func listableCommand() devices.CommandRecord {
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	return devices.CommandRecord{
		ID: apiCommandID, EntityID: apiEntityID, AdapterID: "simulator",
		OperationName: "set", Parameters: devices.CommandParameters(`{"value":true}`),
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab", Status: devices.CommandStatusSatisfied,
		RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(10 * time.Second),
	}
}

func apiStateHistoryEntry(receiveOrder int64) devices.EntityStateHistoryEntry {
	adapterReceivedAt := time.Date(2026, 8, 25, 10, 0, 0, 123456789, time.UTC)
	observedAt := adapterReceivedAt.Add(time.Second)
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	return devices.EntityStateHistoryEntry{
		ObservationID:     apiObservationID,
		Value:             devices.Value(`true`),
		Disposition:       devices.DispositionApplied,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   &sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      receiveOrder,
	}
}

func apiEntityEventEntry(receiveOrder int64) devices.EntityEventHistoryEntry {
	emittedAt := time.Date(2026, 8, 25, 10, 0, 0, 123456789, time.UTC)
	return devices.EntityEventHistoryEntry{
		EventID:      apiEntityEventID,
		EntityID:     apiEntityID,
		Name:         devices.EntityEventName("single_press"),
		Disposition:  devices.EntityEventDispositionAccepted,
		EmittedAt:    emittedAt,
		ReceivedAt:   emittedAt.Add(time.Second),
		RecordedAt:   emittedAt.Add(2 * time.Second),
		ReceiveOrder: receiveOrder,
	}
}

const apiEntityEventID = devices.EntityEventID("evt_01890f47-7a6b-7c4d-8e9f-0123456789ab")
