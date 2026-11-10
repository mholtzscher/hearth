package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	devicesTag  = "Devices"
	entitiesTag = "Entities"
	commandsTag = "Commands"
)

// Register publishes Device operations. A nil logger discards HTTP diagnostics.
func Register(api huma.API, service Devices, presenter *devices.Presenter, logger *slog.Logger) {
	if presenter == nil {
		panic("devices presenter is required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	handler := &handler{devices: service, presenter: presenter, logger: logger}
	registerUpdateDevice(api, handler)
	registerListEntities(api, handler)
	registerGetEntity(api, handler)
	registerUpdateEntity(api, handler)
	registerExecuteEntityCommand(api, handler)
	registerListEntityCommands(api, handler)
	registerListDevices(api, handler)
	registerGetDevice(api, handler)
	registerGetCommand(api, handler)
	registerListCommands(api, handler)
	registerListEntityAvailabilityHistory(api, handler)
	registerListEntityStateHistory(api, handler)
	registerQueryEntityStateHistory(api, handler)
	registerQueryEntityStateChanges(api, handler)
	registerListEntityEvents(api, handler)
}
