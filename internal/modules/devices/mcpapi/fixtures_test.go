package mcpapi //nolint:testpackage // Tests use private transport helpers and domain doubles.

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

const (
	apiAdapterID     = "simulator"
	apiEntityID      = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiDeviceID      = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiObservationID = devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type stubDevices struct {
	queryHistory                  func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error)
	getEntity                     func(context.Context, devices.EntityID) (devices.EntityWithState, error)
	entityOperations              func(devices.Entity) ([]devices.OperationContract, error)
	patchEntity                   func(context.Context, devices.EntityID, devices.EntityPatch) (devices.EntityWithState, error)
	patchDevice                   func(context.Context, devices.DeviceID, devices.DevicePatch) (devices.Device, error)
	listDevices                   func(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	getDevice                     func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	listEntities                  func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	getCommand                    func(context.Context, devices.CommandID) (devices.CommandRecord, error)
	createLabel                   func(context.Context, devices.CreateLabelParams) (devices.Label, error)
	renameLabel                   func(context.Context, devices.LabelID, string) (devices.Label, error)
	deleteLabel                   func(context.Context, devices.LabelID) error
	listLabels                    func(context.Context) ([]devices.Label, error)
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

func testPresenter() *devices.Presenter {
	return devices.NewPresenter(household.UnitSystemMetric, devices.TableConverter{})
}

func (stub *stubDevices) QueryHistory(
	ctx context.Context,
	query devices.HistoryQuery,
) (devices.HistoryQueryResult, error) {
	if stub.queryHistory == nil {
		panic("unexpected QueryHistory call")
	}
	return stub.queryHistory(ctx, query)
}

func (stub *stubDevices) GetEntity(ctx context.Context, entityID devices.EntityID) (devices.EntityWithState, error) {
	if stub.getEntity == nil {
		panic("unexpected GetEntity call")
	}
	return stub.getEntity(ctx, entityID)
}

// EntityOperations defaults to the built-in catalog, so get_entity fixtures
// publish the operations their Entity type and support actually enable.
func (stub *stubDevices) EntityOperations(entity devices.Entity) ([]devices.OperationContract, error) {
	if stub.entityOperations != nil {
		return stub.entityOperations(entity)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		return nil, err
	}
	return catalog.SupportedOperations(entity)
}

func (stub *stubDevices) PatchEntity(
	ctx context.Context,
	entityID devices.EntityID,
	patch devices.EntityPatch,
) (devices.EntityWithState, error) {
	if stub.patchEntity == nil {
		panic("unexpected PatchEntity call")
	}
	return stub.patchEntity(ctx, entityID, patch)
}

func (stub *stubDevices) PatchDevice(
	ctx context.Context,
	deviceID devices.DeviceID,
	patch devices.DevicePatch,
) (devices.Device, error) {
	if stub.patchDevice == nil {
		panic("unexpected PatchDevice call")
	}
	return stub.patchDevice(ctx, deviceID, patch)
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

func (stub *stubDevices) CreateLabel(ctx context.Context, params devices.CreateLabelParams) (devices.Label, error) {
	if stub.createLabel == nil {
		panic("unexpected CreateLabel call")
	}
	return stub.createLabel(ctx, params)
}

func (stub *stubDevices) RenameLabel(ctx context.Context, id devices.LabelID, name string) (devices.Label, error) {
	if stub.renameLabel == nil {
		panic("unexpected RenameLabel call")
	}
	return stub.renameLabel(ctx, id, name)
}

func (stub *stubDevices) DeleteLabel(ctx context.Context, id devices.LabelID) error {
	if stub.deleteLabel == nil {
		panic("unexpected DeleteLabel call")
	}
	return stub.deleteLabel(ctx, id)
}

func (stub *stubDevices) ListLabels(ctx context.Context) ([]devices.Label, error) {
	if stub.listLabels == nil {
		panic("unexpected ListLabels call")
	}
	return stub.listLabels(ctx)
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

const (
	apiAreaLabelID = devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	apiTagLabelID  = devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789a2")
)

func apiAreaRef() devices.LabelRef { return devices.LabelRef{ID: apiAreaLabelID, Name: "Kitchen"} }

func apiTagRef() devices.LabelRef { return devices.LabelRef{ID: apiTagLabelID, Name: "Lamps"} }

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
