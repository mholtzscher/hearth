package api

import (
	"context"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Devices interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
	SetEntityEnabled(context.Context, devices.EntityID, bool) (devices.EntityWithState, error)
	ExecuteCommand(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error)
	ListDevices(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	GetDevice(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	ListEntities(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
	ListEntityCommands(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
	ListAdapters(context.Context, devices.ListAdaptersParams) (devices.Page[devices.AdapterInstance], error)
	GetAdapter(context.Context, string) (devices.AdapterInstance, error)
	ListAdapterHealthHistory(
		context.Context,
		devices.ListAdapterHealthParams,
	) (devices.Page[devices.HealthTransition], error)
	ListEntityAvailabilityHistory(
		context.Context,
		devices.ListEntityAvailabilityParams,
	) (devices.Page[devices.HealthTransition], error)
	ListEntityStateHistory(
		context.Context,
		devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error)
	ListEntityDeviceEvents(
		context.Context,
		devices.ListEntityDeviceEventsParams,
	) (devices.Page[devices.DeviceEventHistoryEntry], error)
}

type Handler struct {
	devices Devices
}

func Register(api huma.API, service Devices) {
	const (
		entitiesTag = "Entities"
		adaptersTag = "Adapters"
	)
	handler := &Handler{devices: service}
	disabledProblemSchema := huma.SchemaFromType(
		api.OpenAPI().Components.Schemas, reflect.TypeFor[disabledCommandError](),
	)
	huma.Register(api, huma.Operation{
		OperationID: "list-entities", Method: http.MethodGet, Path: "/entities",
		Summary: "List Entities and their current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.ListEntities)
	huma.Register(api, huma.Operation{
		OperationID: "get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}",
		Summary: "Get an Entity and its current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetEntity)
	huma.Register(api, huma.Operation{
		OperationID: "update-entity", Method: http.MethodPatch, Path: "/entities/{entity_id}",
		Summary: "Update an Entity", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.PatchEntity)
	huma.Register(api, huma.Operation{
		OperationID: "execute-entity-command", Method: http.MethodPost, Path: "/entities/{entity_id}/commands",
		Summary: "Execute an Entity Command", Tags: []string{entitiesTag},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError,
		},
		Responses: map[string]*huma.Response{
			"409": {
				Description: http.StatusText(http.StatusConflict),
				Content: map[string]*huma.MediaType{
					"application/problem+json": {Schema: disabledProblemSchema},
				},
			},
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
	huma.Register(api, huma.Operation{
		OperationID: "list-adapters", Method: http.MethodGet, Path: "/adapters",
		Summary: "List Adapters and their current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.ListAdapters)
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter", Method: http.MethodGet, Path: "/adapters/{adapter_id}",
		Summary: "Get an Adapter and its current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetAdapter)
	huma.Register(api, huma.Operation{
		OperationID: "list-adapter-health-history", Method: http.MethodGet,
		Path: "/adapters/{adapter_id}/health/history", Summary: "List an Adapter's health history",
		Tags:   []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListAdapterHealthHistory)
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-availability-history", Method: http.MethodGet,
		Path: "/entities/{entity_id}/availability/history", Summary: "List an Entity's availability history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListEntityAvailabilityHistory)
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-state-history", Method: http.MethodGet,
		Path: "/entities/{entity_id}/state/history", Summary: "List an Entity's State history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListEntityStateHistory)
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-device-events", Method: http.MethodGet,
		Path: "/entities/{entity_id}/events", Summary: "List an Entity's Device Event history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListEntityDeviceEvents)
}
