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
	handler.registerTools(server)
	handler.registerResources(server)
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

// registerTools registers the whole Devices tool catalog, grouped by the
// Hearth read or action each tool exposes.
func (handler *Handler) registerTools(server *mcpapi.Server) {
	handler.registerEntityTools(server)
	handler.registerEntityHistoryTools(server)
	handler.registerDeviceTools(server)
	handler.registerCommandTools(server)
	handler.registerAdapterTools(server)
}

// registerEntityTools registers the Entity state and action tools.
func (handler *Handler) registerEntityTools(server *mcpapi.Server) {
	mcpapi.Register(server, mcpapi.Tool[mcpListEntitiesInput, EntityCollectionBody]{
		Name: mcpToolListEntities, Description: "List Entities and their current State",
		Handler: handler.listEntities,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetEntityInput, EntityBody]{
		Name: mcpToolGetEntity, Description: "Get an Entity and its current State",
		Handler: handler.getEntity,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpUpdateEntityInput, EntityBody]{
		Name: mcpToolUpdateEntity, Description: "Update an Entity",
		Handler: handler.updateEntity,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpExecuteEntityCommandInput, CommandResultBody]{
		Name: mcpToolExecuteEntityCommand, Description: "Execute an Entity Command",
		Handler: handler.executeEntityCommand,
	})
}

// registerEntityHistoryTools registers the Entity history read tools.
func (handler *Handler) registerEntityHistoryTools(server *mcpapi.Server) {
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityCommandsInput, CommandCollectionBody]{
		Name: mcpToolListEntityCommands, Description: "List an Entity's Command history",
		Handler: handler.listEntityCommands,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityStateHistoryInput, EntityStateHistoryCollectionBody]{
		Name:        mcpToolListEntityStateHistory,
		Description: "List an Entity's State history",
		Handler:     handler.listEntityStateHistory,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityEventsInput, EntityEventCollectionBody]{
		Name: mcpToolListEntityEvents, Description: "List an Entity's Entity Event history",
		Handler: handler.listEntityEvents,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListEntityAvailabilityHistoryInput, HealthTransitionCollectionBody]{
		Name: mcpToolListEntityAvailabilityHistory, Description: "List an Entity's availability history",
		Handler: handler.listEntityAvailabilityHistory,
	})
}

// registerDeviceTools registers the Device read tools.
func (handler *Handler) registerDeviceTools(server *mcpapi.Server) {
	mcpapi.Register(server, mcpapi.Tool[mcpListDevicesInput, DeviceCollectionBody]{
		Name: mcpToolListDevices, Description: "List Devices",
		Handler: handler.listDevices,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetDeviceInput, DeviceDetailBody]{
		Name: mcpToolGetDevice, Description: "Get a Device and its Entities",
		Handler: handler.getDevice,
	})
}

// registerCommandTools registers the household Command read tools.
func (handler *Handler) registerCommandTools(server *mcpapi.Server) {
	mcpapi.Register(server, mcpapi.Tool[mcpGetCommandInput, CommandRecordBody]{
		Name: mcpToolGetCommand, Description: "Get a Command record",
		Handler: handler.getCommand,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListCommandsInput, CommandCollectionBody]{
		Name: mcpToolListCommands, Description: "List household Command history",
		Handler: handler.listCommands,
	})
}

// registerAdapterTools registers the Adapter read tools.
func (handler *Handler) registerAdapterTools(server *mcpapi.Server) {
	mcpapi.Register(server, mcpapi.Tool[mcpListAdaptersInput, AdapterCollectionBody]{
		Name: mcpToolListAdapters, Description: "List Adapters and their current health",
		Handler: handler.listAdapters,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetAdapterInput, AdapterBody]{
		Name: mcpToolGetAdapter, Description: "Get an Adapter and its current health",
		Handler: handler.getAdapter,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListAdapterHealthHistoryInput, HealthTransitionCollectionBody]{
		Name: mcpToolListAdapterHealthHistory, Description: "List an Adapter's health history",
		Handler: handler.listAdapterHealthHistory,
	})
}

// listEntities lists one page of Entities, optionally scoped to a Device.
func (handler *Handler) listEntities(
	ctx context.Context,
	input mcpListEntitiesInput,
) (EntityCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListEntities, &ListEntitiesInput{
		Limit: mcpPageSize(input.Limit), Cursor: input.Cursor, DeviceID: mcpOptionalDeviceID(input.DeviceID),
	}, mcpFailureNone)
	if err != nil {
		return EntityCollectionBody{}, err
	}
	return output.Body, nil
}

// getEntity reads one Entity and its current State.
func (handler *Handler) getEntity(ctx context.Context, input mcpGetEntityInput) (EntityBody, error) {
	output, err := mcpRead(ctx, handler.GetEntity, &GetEntityInput{EntityID: string(input.EntityID)},
		mcpFailureEntityNotFound)
	if err != nil {
		return EntityBody{}, err
	}
	return output.Body, nil
}

// updateEntity sets one Entity's enabled flag.
func (handler *Handler) updateEntity(ctx context.Context, input mcpUpdateEntityInput) (EntityBody, error) {
	output, err := mcpRead(ctx, handler.PatchEntity, &PatchEntityInput{
		EntityID: string(input.EntityID), Body: PatchEntityBody{Enabled: input.Enabled},
	}, mcpFailureEntityNotFound)
	if err != nil {
		return EntityBody{}, err
	}
	return output.Body, nil
}

// executeEntityCommand blocks until the Command reaches its terminal outcome or
// the operation deadline fails it, exactly as
// POST /v1/entities/{entity_id}/commands does, so a dropped call still leaves a
// durable record readable with get_command.
//
// Unlike the read tools it calls the service directly: the durable Command
// failure code and status cross the MCP boundary as structured error fields, and
// the Huma error mapping keeps only an HTTP status and prose.
func (handler *Handler) executeEntityCommand(
	ctx context.Context,
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

// listEntityCommands lists one page of an Entity's Command history.
func (handler *Handler) listEntityCommands(
	ctx context.Context,
	input mcpListEntityCommandsInput,
) (CommandCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListEntityCommands, &ListEntityCommandsInput{
		EntityID: string(input.EntityID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return CommandCollectionBody{}, err
	}
	return output.Body, nil
}

// listEntityStateHistory lists one page of an Entity's retained State history.
func (handler *Handler) listEntityStateHistory(
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
}

// listEntityEvents lists one page of an Entity's Entity Event history.
func (handler *Handler) listEntityEvents(
	ctx context.Context,
	input mcpListEntityEventsInput,
) (EntityEventCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListEntityEvents, &ListEntityEventsInput{
		EntityID: string(input.EntityID), Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return EntityEventCollectionBody{}, err
	}
	return output.Body, nil
}

// listEntityAvailabilityHistory lists one page of an Entity's availability
// history.
func (handler *Handler) listEntityAvailabilityHistory(
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
}

// listDevices lists one page of Devices.
func (handler *Handler) listDevices(
	ctx context.Context,
	input mcpListDevicesInput,
) (DeviceCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListDevices, &ListDevicesInput{
		Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return DeviceCollectionBody{}, err
	}
	return output.Body, nil
}

// getDevice reads one Device and the first page of its Entities.
func (handler *Handler) getDevice(ctx context.Context, input mcpGetDeviceInput) (DeviceDetailBody, error) {
	output, err := mcpRead(ctx, handler.GetDevice, &GetDeviceInput{
		DeviceID: string(input.DeviceID), EntityLimit: mcpPageSize(input.EntityLimit),
		EntityCursor: input.EntityCursor,
	}, mcpFailureDeviceNotFound)
	if err != nil {
		return DeviceDetailBody{}, err
	}
	return output.Body, nil
}

// getCommand reads one Command record.
func (handler *Handler) getCommand(ctx context.Context, input mcpGetCommandInput) (CommandRecordBody, error) {
	output, err := mcpRead(ctx, handler.GetCommand, &GetCommandInput{CommandID: string(input.CommandID)},
		mcpFailureCommandNotFound)
	if err != nil {
		return CommandRecordBody{}, err
	}
	return output.Body, nil
}

// listCommands lists one page of household Command history.
func (handler *Handler) listCommands(
	ctx context.Context,
	input mcpListCommandsInput,
) (CommandCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListCommands, &ListCommandsInput{
		Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
		EntityID: mcpOptionalEntityID(input.EntityID), Status: input.Status,
	}, mcpFailureNone)
	if err != nil {
		return CommandCollectionBody{}, err
	}
	return output.Body, nil
}

// listAdapters lists one page of Adapters and their current health.
func (handler *Handler) listAdapters(
	ctx context.Context,
	input mcpListAdaptersInput,
) (AdapterCollectionBody, error) {
	output, err := mcpRead(ctx, handler.ListAdapters, &ListAdaptersInput{
		Limit: mcpPageSize(input.Limit), Cursor: input.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return AdapterCollectionBody{}, err
	}
	return output.Body, nil
}

// getAdapter reads one Adapter and its current health.
func (handler *Handler) getAdapter(ctx context.Context, input mcpGetAdapterInput) (AdapterBody, error) {
	output, err := mcpRead(ctx, handler.GetAdapter, &GetAdapterInput{AdapterID: string(input.AdapterID)},
		mcpFailureAdapterNotFound)
	if err != nil {
		return AdapterBody{}, err
	}
	return output.Body, nil
}

// listAdapterHealthHistory lists one page of an Adapter's health history.
func (handler *Handler) listAdapterHealthHistory(
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
