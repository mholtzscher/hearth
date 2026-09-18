package api

import (
	"context"
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The 15 Devices MCP Tool names mirror the Huma operationId of the operation
// they expose, in snake_case. Tool names are transport vocabulary: the Hearth
// Operation stays a tool argument.
const (
	mcpToolListEntities                  = "list_entities"
	mcpToolGetEntity                     = "get_entity"
	mcpToolUpdateEntity                  = "update_entity"
	mcpToolExecuteEntityCommand          = "execute_entity_command"
	mcpToolListEntityCommands            = "list_entity_commands"
	mcpToolListDevices                   = "list_devices"
	mcpToolGetDevice                     = "get_device"
	mcpToolGetCommand                    = "get_command"
	mcpToolListCommands                  = "list_commands"
	mcpToolListAdapters                  = "list_adapters"
	mcpToolGetAdapter                    = "get_adapter"
	mcpToolListAdapterHealthHistory      = "list_adapter_health_history"
	mcpToolListEntityAvailabilityHistory = "list_entity_availability_history"
	mcpToolListEntityStateHistory        = "list_entity_state_history"
	mcpToolListEntityEvents              = "list_entity_events"
)

// RegisterMCP registers every Devices MCP Tool and resource on server.
//
// It is the MCP counterpart of Register: the same Huma operations, reachable
// over MCP with equal semantics. Each tool is a thin translation from its flat
// MCP arguments to the shared Huma request, so a tool, its resource, and its
// HTTP route read through one service call and one response mapping.
func RegisterMCP(server *mcpapi.Server, service Devices) {
	handler := &Handler{devices: service}
	registerMCPTools(server, handler)
	registerMCPResources(server, handler)
}

// registerMCPTools registers the whole Devices tool catalog, grouped by the
// Hearth read or action each tool exposes.
func registerMCPTools(server *mcpapi.Server, handler *Handler) {
	registerMCPEntityTools(server, handler)
	registerMCPEntityHistoryTools(server, handler)
	registerMCPDeviceTools(server, handler)
	registerMCPCommandTools(server, handler)
	registerMCPAdapterTools(server, handler)
}

// mcpRead calls one shared Huma read operation and maps a failure to the tool
// error an agent branches on.
//
// Reusing the Huma handler keeps one translation per Hearth read: the tool, the
// resource, and the HTTP route share the same cursor decode, page default, body
// mapping, and failure classification. Only the missing-parent code differs per
// read, because the domain sentinel is gone by the time Huma has mapped it.
func mcpRead[I, O any](
	ctx context.Context,
	read func(context.Context, *I) (*O, error),
	input *I,
	missing mcpFailureCode,
) (*O, error) {
	output, err := read(ctx, input)
	if err != nil {
		return nil, mcpReadFailure(err, missing)
	}
	if output == nil {
		return nil, mcpInternalFailure()
	}
	return output, nil
}

// registerMCPEntityTools registers the Entity state and action tools.
func registerMCPEntityTools(server *mcpapi.Server, handler *Handler) {
	mcpapi.Register(server, mcpapi.Tool[mcpListEntitiesInput, EntityCollectionBody]{
		Name: mcpToolListEntities, Description: "List Entities and their current State",
		Handler: func(ctx context.Context, input mcpListEntitiesInput) (EntityCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListEntities, &ListEntitiesInput{
				Limit: mcpPageSize(input.Limit), Cursor: input.Cursor, DeviceID: mcpOptionalDeviceID(input.DeviceID),
			}, mcpFailureNone)
			if err != nil {
				return EntityCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetEntityInput, EntityBody]{
		Name: mcpToolGetEntity, Description: "Get an Entity and its current State",
		Handler: func(ctx context.Context, input mcpGetEntityInput) (EntityBody, error) {
			output, err := mcpRead(ctx, handler.GetEntity, &GetEntityInput{EntityID: string(input.EntityID)},
				mcpFailureEntityNotFound)
			if err != nil {
				return EntityBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpUpdateEntityInput, EntityBody]{
		Name: mcpToolUpdateEntity, Description: "Update an Entity",
		Handler: func(ctx context.Context, input mcpUpdateEntityInput) (EntityBody, error) {
			output, err := mcpRead(ctx, handler.PatchEntity, &PatchEntityInput{
				EntityID: string(input.EntityID), Body: PatchEntityBody{Enabled: input.Enabled},
			}, mcpFailureEntityNotFound)
			if err != nil {
				return EntityBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpExecuteEntityCommandInput, CommandResultBody]{
		Name: mcpToolExecuteEntityCommand, Description: "Execute an Entity Command",
		Handler: func(ctx context.Context, input mcpExecuteEntityCommandInput) (CommandResultBody, error) {
			return mcpExecuteEntityCommand(ctx, handler, input)
		},
	})
}

// registerMCPEntityHistoryTools registers the Entity history read tools.
func registerMCPEntityHistoryTools(server *mcpapi.Server, handler *Handler) {
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityCommandsInput, CommandCollectionBody]{
		Name: mcpToolListEntityCommands, Description: "List an Entity's Command history",
		Handler: func(ctx context.Context, input mcpListEntityCommandsInput) (CommandCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListEntityCommands, &ListEntityCommandsInput{
				EntityID: string(input.EntityID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureEntityNotFound)
			if err != nil {
				return CommandCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityStateHistoryInput, EntityStateHistoryCollectionBody]{
		Name:        mcpToolListEntityStateHistory,
		Description: "List an Entity's State history",
		Handler: func(
			ctx context.Context,
			input mcpListEntityStateHistoryInput,
		) (EntityStateHistoryCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListEntityStateHistory, &ListEntityStateHistoryInput{
				EntityID: string(input.EntityID), Disposition: input.Disposition,
				Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureEntityNotFound)
			if err != nil {
				return EntityStateHistoryCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityEventsInput, EntityEventCollectionBody]{
		Name: mcpToolListEntityEvents, Description: "List an Entity's Entity Event history",
		Handler: func(ctx context.Context, input mcpListEntityEventsInput) (EntityEventCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListEntityEvents, &ListEntityEventsInput{
				EntityID: string(input.EntityID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureEntityNotFound)
			if err != nil {
				return EntityEventCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityAvailabilityHistoryInput, HealthTransitionCollectionBody]{
		Name: mcpToolListEntityAvailabilityHistory, Description: "List an Entity's availability history",
		Handler: func(
			ctx context.Context,
			input mcpListEntityAvailabilityHistoryInput,
		) (HealthTransitionCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListEntityAvailabilityHistory, &ListEntityAvailabilityHistoryInput{
				EntityID: string(input.EntityID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureEntityNotFound)
			if err != nil {
				return HealthTransitionCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
}

// registerMCPDeviceTools registers the Device read tools.
func registerMCPDeviceTools(server *mcpapi.Server, handler *Handler) {
	mcpapi.Register(server, mcpapi.Tool[mcpListDevicesInput, DeviceCollectionBody]{
		Name: mcpToolListDevices, Description: "List Devices",
		Handler: func(ctx context.Context, input mcpListDevicesInput) (DeviceCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListDevices, &ListDevicesInput{
				Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureNone)
			if err != nil {
				return DeviceCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetDeviceInput, DeviceDetailBody]{
		Name: mcpToolGetDevice, Description: "Get a Device and its Entities",
		Handler: func(ctx context.Context, input mcpGetDeviceInput) (DeviceDetailBody, error) {
			output, err := mcpRead(ctx, handler.GetDevice, &GetDeviceInput{
				DeviceID: string(input.DeviceID), EntityLimit: mcpPageSize(input.EntityLimit),
				EntityCursor: input.EntityCursor,
			}, mcpFailureDeviceNotFound)
			if err != nil {
				return DeviceDetailBody{}, err
			}
			return output.Body, nil
		},
	})
}

// registerMCPCommandTools registers the household Command read tools.
func registerMCPCommandTools(server *mcpapi.Server, handler *Handler) {
	mcpapi.Register(server, mcpapi.Tool[mcpGetCommandInput, CommandRecordBody]{
		Name: mcpToolGetCommand, Description: "Get a Command record",
		Handler: func(ctx context.Context, input mcpGetCommandInput) (CommandRecordBody, error) {
			output, err := mcpRead(ctx, handler.GetCommand, &GetCommandInput{CommandID: string(input.CommandID)},
				mcpFailureCommandNotFound)
			if err != nil {
				return CommandRecordBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListCommandsInput, CommandCollectionBody]{
		Name: mcpToolListCommands, Description: "List household Command history",
		Handler: func(ctx context.Context, input mcpListCommandsInput) (CommandCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListCommands, &ListCommandsInput{
				Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
				EntityID: mcpOptionalEntityID(input.EntityID), Status: input.Status,
			}, mcpFailureNone)
			if err != nil {
				return CommandCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
}

// registerMCPAdapterTools registers the Adapter read tools.
func registerMCPAdapterTools(server *mcpapi.Server, handler *Handler) {
	mcpapi.Register(server, mcpapi.Tool[mcpListAdaptersInput, AdapterCollectionBody]{
		Name: mcpToolListAdapters, Description: "List Adapters and their current health",
		Handler: func(ctx context.Context, input mcpListAdaptersInput) (AdapterCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListAdapters, &ListAdaptersInput{
				Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureNone)
			if err != nil {
				return AdapterCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetAdapterInput, AdapterBody]{
		Name: mcpToolGetAdapter, Description: "Get an Adapter and its current health",
		Handler: func(ctx context.Context, input mcpGetAdapterInput) (AdapterBody, error) {
			output, err := mcpRead(ctx, handler.GetAdapter, &GetAdapterInput{AdapterID: string(input.AdapterID)},
				mcpFailureAdapterNotFound)
			if err != nil {
				return AdapterBody{}, err
			}
			return output.Body, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListAdapterHealthHistoryInput, HealthTransitionCollectionBody]{
		Name: mcpToolListAdapterHealthHistory, Description: "List an Adapter's health history",
		Handler: func(
			ctx context.Context,
			input mcpListAdapterHealthHistoryInput,
		) (HealthTransitionCollectionBody, error) {
			output, err := mcpRead(ctx, handler.ListAdapterHealthHistory, &ListAdapterHealthHistoryInput{
				AdapterID: string(input.AdapterID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
			}, mcpFailureAdapterNotFound)
			if err != nil {
				return HealthTransitionCollectionBody{}, err
			}
			return output.Body, nil
		},
	})
}

// mcpExecuteEntityCommand blocks until the Command reaches its terminal outcome
// or the operation deadline fails it, exactly as
// POST /v1/entities/{entity_id}/commands does, so a dropped call still leaves a
// durable record readable with get_command.
//
// Unlike the read tools it calls the service directly: the durable Command
// failure code and status cross the MCP boundary as structured error fields, and
// the Huma error mapping keeps only an HTTP status and prose.
func mcpExecuteEntityCommand(
	ctx context.Context,
	handler *Handler,
	input mcpExecuteEntityCommandInput,
) (CommandResultBody, error) {
	parameters, err := json.Marshal(input.Parameters)
	if err != nil {
		return CommandResultBody{}, mcpInvalidRequestFailure("parameters must be a JSON object")
	}
	result, err := handler.devices.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      devices.EntityID(input.EntityID),
		OperationName: devices.OperationName(input.Operation),
		Parameters:    devices.CommandParameters(parameters),
	})
	if err != nil {
		return CommandResultBody{}, mcpCommandFailure(err)
	}
	return mcpCommandResultBody(result)
}

// mcpCommandResultBody maps one terminal Command result to the body the HTTP
// route and the tool both return. The observed case carries the evidence; the
// dispatched case omits it.
func mcpCommandResultBody(result devices.CommandResult) (CommandResultBody, error) {
	if result.Outcome == devices.OutcomeDispatched {
		return CommandResultBody{
			CommandID: string(result.CommandID), Status: mcpCommandStatusDispatched,
		}, nil
	}
	if result.ObservationID == nil || result.Value == nil {
		return CommandResultBody{}, mcpInternalFailure()
	}
	var value any
	if err := decodeJSON(*result.Value, &value); err != nil {
		return CommandResultBody{}, mcpInternalFailure()
	}
	observationID := string(*result.ObservationID)
	return CommandResultBody{
		CommandID: string(result.CommandID), Status: mcpCommandStatusSatisfied,
		ObservationID: &observationID, Value: &value,
	}, nil
}
