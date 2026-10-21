package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/mcpapi"

func Register(server *mcptools.Server, service Devices) {
	h := &handler{devices: service}
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
	registerListEntityEvents(server, h)
}
