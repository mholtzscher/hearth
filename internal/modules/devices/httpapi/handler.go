package httpapi

import (
	"context"
	"log/slog"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Devices interface {
	ListLabels(context.Context) ([]devices.Label, error)
	CreateLabel(context.Context, devices.CreateLabelParams) (devices.Label, error)
	RenameLabel(context.Context, devices.LabelID, string) (devices.Label, error)
	DeleteLabel(context.Context, devices.LabelID) error
	QueryHistory(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error)
	QueryStateChanges(context.Context, devices.StateChangeQuery) (devices.StateChangeQueryResult, error)
	PatchEntity(context.Context, devices.EntityID, devices.EntityPatch) (devices.EntityWithState, error)
	PatchDevice(context.Context, devices.DeviceID, devices.DevicePatch) (devices.Device, error)
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
	FetchArtwork(context.Context, devices.EntityID) (devices.Artwork, error)
	ArtworkETag(context.Context, devices.EntityID) (string, error)
	ExecuteCommand(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error)
	ListDevices(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	GetDevice(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	ListEntities(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
	ListEntityCommands(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
	ListCommands(context.Context, devices.ListCommandsParams) (devices.Page[devices.CommandRecord], error)
	ListEntityAvailabilityHistory(
		context.Context,
		devices.ListEntityAvailabilityParams,
	) (devices.Page[devices.HealthTransition], error)
	ListEntityStateHistory(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error)
	ListEntityEvents(
		context.Context,
		devices.ListEntityEventsParams,
	) (devices.Page[devices.EntityEventHistoryEntry], error)
}

type handler struct {
	devices   Devices
	presenter *devices.Presenter
	logger    *slog.Logger
}
