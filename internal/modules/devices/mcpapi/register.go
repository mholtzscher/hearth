package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"

func mustContract[T any](contract T, err error) T {
	if err != nil {
		panic(err)
	}
	return contract
}

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
