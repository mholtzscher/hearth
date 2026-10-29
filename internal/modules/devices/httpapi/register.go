package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

const (
	devicesTag  = "Devices"
	entitiesTag = "Entities"
	commandsTag = "Commands"
)

// Register publishes Device operations. A nil logger discards HTTP diagnostics.
func Register(api huma.API, service Devices, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	handler := &handler{devices: service, logger: logger}
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
	registerListEntityEvents(api, handler)
}
