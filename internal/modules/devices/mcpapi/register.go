package mcpapi

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func Register(server *mcptools.Server, service Devices, presenter *devices.Presenter) {
	if presenter == nil {
		panic("devices presenter is required")
	}
	h := &handler{devices: service, presenter: presenter}
	registerListEntities(server, h)
	registerGetEntity(server, h)
	registerUpdateEntity(server, h)
	registerExecuteEntityCommand(server, h)
	registerListEntityCommands(server, h)
	registerListDevices(server, h)
	registerGetDevice(server, h)
	registerGetCommand(server, h)
	registerListCommands(server, h)
	registerListEntityAvailabilityHistory(server, h)
	registerListEntityStateHistory(server, h)
	registerQueryEntityStateHistory(server, h)
	registerListEntityEvents(server, h)
}
