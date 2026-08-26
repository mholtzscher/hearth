package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Devices interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
	ExecuteCommand(context.Context, devices.EntityID, devices.OperationName, devices.CommandParameters) (devices.CommandResult, error)
	ListDevices(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	GetDevice(context.Context, devices.DeviceID) (devices.DeviceAggregate, error)
	ListEntities(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
	ListEntityCommands(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
}

type Handler struct {
	devices Devices
}

func Register(api huma.API, service Devices) {
	handler := &Handler{devices: service}
	huma.Register(api, huma.Operation{
		OperationID: "list-entities", Method: http.MethodGet, Path: "/entities",
		Summary: "List Entities and their current State", Tags: []string{"Entities"},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.ListEntities)
	huma.Register(api, huma.Operation{
		OperationID: "get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}",
		Summary: "Get an Entity and its current State", Tags: []string{"Entities"},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetEntity)
	huma.Register(api, huma.Operation{
		OperationID: "execute-entity-command", Method: http.MethodPost, Path: "/entities/{entity_id}/commands",
		Summary: "Execute an Entity Command", Tags: []string{"Entities"},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError,
		},
	}, handler.ExecuteCommand)
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-commands", Method: http.MethodGet, Path: "/entities/{entity_id}/commands",
		Summary: "List an Entity's Command history", Tags: []string{"Commands"},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListEntityCommands)
	huma.Register(api, huma.Operation{
		OperationID: "list-devices", Method: http.MethodGet, Path: "/devices",
		Summary: "List Devices", Tags: []string{"Devices"},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.ListDevices)
	huma.Register(api, huma.Operation{
		OperationID: "get-device", Method: http.MethodGet, Path: "/devices/{device_id}",
		Summary: "Get a Device and its Entities", Tags: []string{"Devices"},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetDevice)
	huma.Register(api, huma.Operation{
		OperationID: "get-command", Method: http.MethodGet, Path: "/commands/{command_id}",
		Summary: "Get a Command record", Tags: []string{"Commands"},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetCommand)
}
