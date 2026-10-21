package httpapi

import "github.com/danielgtaylor/huma/v2"

const (
	devicesTag  = "Devices"
	entitiesTag = "Entities"
	commandsTag = "Commands"
)

func Register(api huma.API, service Devices) {
	handler := &handler{devices: service}
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
