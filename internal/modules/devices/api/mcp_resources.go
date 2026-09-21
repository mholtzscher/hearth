package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The hearth:// namespace is read-only and poll-only: one template per catalog
// entry, each reading through the same Huma handler the matching tool uses, so a
// resource returns the exact JSON body the matching Huma GET returns. The
// scheme, JSON body MIME type, and page parameters that every resource module
// shares come from mcpapi.
const (
	// mcpResourceKind* are the URI hosts of the resources this package serves.
	mcpResourceKindEntity   = "entity"
	mcpResourceKindDevice   = "device"
	mcpResourceKindAdapter  = "adapter"
	mcpResourceKindCommand  = "command"
	mcpResourceKindEntities = "entities"
	mcpResourceKindDevices  = "devices"
	mcpResourceKindAdapters = "adapters"
	mcpResourceKindCommands = "commands"

	// mcpResourceSuffix* are the fixed path suffixes that follow an owner ID.
	mcpResourceSuffixEvents       = "events"
	mcpResourceSuffixCommands     = "commands"
	mcpResourceSuffixStateHistory = "state/history"
	mcpResourceSuffixAvailability = "availability/history"
	mcpResourceSuffixHealth       = "health/history"

	// mcpQuery* are the query parameters the resource templates accept. The page
	// parameters every paginated resource shares are mcpapi.ResourceQueryLimit and
	// mcpapi.ResourceQueryCursor.
	mcpQueryFilter   = "filter"
	mcpQueryStatus   = "status"
	mcpQueryEntityID = "entity_id"
	mcpQueryDeviceID = "device_id"
)

// Resource templates as the MCP resource catalog lists them.
//
// Entity Command history accepts the catalog's `status` filter, which
// ListEntityCommands does not take: an unfiltered read stays on it while a
// status-filtered read routes to the household ListCommands read, which already
// scopes by Entity and status with the same ordering, page size, and opaque
// cursor scheme. Widening the shared read interface instead would touch the
// domain, repository, SQL, Huma route, and cursor for one resource filter.
//
// The Device template carries no embedded-Entity paging parameters, so it reads
// the default first page exactly as GET /v1/devices/{device_id} does; the
// get_device tool exposes entity_limit and entity_cursor for deeper pages.
const (
	mcpEntityURITemplate             = "hearth://entity/{entity_id}"
	mcpEntityStateHistoryURITemplate = "hearth://entity/{entity_id}/state/history{?filter,cursor,limit}"
	mcpEntityEventsURITemplate       = "hearth://entity/{entity_id}/events{?cursor,limit}"
	mcpEntityCommandsURITemplate     = "hearth://entity/{entity_id}/commands{?status,cursor,limit}"
	mcpEntityAvailabilityURITemplate = "hearth://entity/{entity_id}/availability/history{?cursor,limit}"
	mcpDeviceURITemplate             = "hearth://device/{device_id}"
	mcpAdapterURITemplate            = "hearth://adapter/{adapter_id}"
	mcpAdapterHealthURITemplate      = "hearth://adapter/{adapter_id}/health/history{?cursor,limit}"
	mcpCommandURITemplate            = "hearth://command/{command_id}"
	mcpCommandsURITemplate           = "hearth://commands{?entity_id,status,cursor,limit}"
	mcpEntitiesURITemplate           = "hearth://entities{?device_id,cursor,limit}"
	mcpDevicesURITemplate            = "hearth://devices{?cursor,limit}"
	mcpAdaptersURITemplate           = "hearth://adapters{?cursor,limit}"
)

// mcpCollectionResourceURIs are the parameterless collection URIs served as
// concrete resources alongside their templates, because clients that
// materialize resources only see concrete resources. Each bare URI reads the
// default first page; paged reads keep flowing through the templates, and both
// entries dispatch to the same collection reader. Parameterized families stay
// template-only, because their URIs cannot name one concrete resource.
const (
	mcpAdaptersURI = "hearth://adapters"
	mcpEntitiesURI = "hearth://entities"
	mcpDevicesURI  = "hearth://devices"
	mcpCommandsURI = "hearth://commands"
)

func (handler *Handler) registerResources(server *mcpapi.Server) {
	register := func(name, description, uriTemplate string, read mcpapi.ResourceRead) {
		mcpapi.RegisterResourceTemplate(server, name, description, uriTemplate,
			mcpapi.ResourceMIMEType, read, mcpResourceFailure)
	}
	registerConcrete := func(name, description, uri string, read mcpapi.ResourceRead) {
		mcpapi.RegisterResource(server, name, description, uri,
			mcpapi.ResourceMIMEType, read, mcpResourceFailure)
	}
	register("entity", "One Entity and its current State",
		mcpEntityURITemplate, handler.mcpEntityResource)
	register("entity_state_history", "One Entity's retained State history page",
		mcpEntityStateHistoryURITemplate, handler.mcpEntityResource)
	register("entity_events", "One Entity's Entity Event history page",
		mcpEntityEventsURITemplate, handler.mcpEntityResource)
	register("entity_commands", "One Entity's Command history page",
		mcpEntityCommandsURITemplate, handler.mcpEntityResource)
	register("entity_availability_history", "One Entity's availability history page",
		mcpEntityAvailabilityURITemplate, handler.mcpEntityResource)
	register("device", "One Device and the first page of its Entities",
		mcpDeviceURITemplate, handler.mcpDeviceResource)
	register("adapter", "One Adapter and its current health",
		mcpAdapterURITemplate, handler.mcpAdapterResource)
	register("adapter_health_history", "One Adapter's health history page",
		mcpAdapterHealthURITemplate, handler.mcpAdapterResource)
	register("command", "One Command record",
		mcpCommandURITemplate, handler.mcpCommandResource)
	register("commands", "One page of household Command history",
		mcpCommandsURITemplate, handler.mcpCollectionResource)
	register("entities", "One page of Entities and their current State",
		mcpEntitiesURITemplate, handler.mcpCollectionResource)
	register("devices", "One page of Devices",
		mcpDevicesURITemplate, handler.mcpCollectionResource)
	register("adapters", "One page of Adapters and their current health",
		mcpAdaptersURITemplate, handler.mcpCollectionResource)
	registerConcrete("adapters", "One page of Adapters and their current health",
		mcpAdaptersURI, handler.mcpCollectionResource)
	registerConcrete("entities", "One page of Entities and their current State",
		mcpEntitiesURI, handler.mcpCollectionResource)
	registerConcrete("devices", "One page of Devices",
		mcpDevicesURI, handler.mcpCollectionResource)
	registerConcrete("commands", "One page of household Command history",
		mcpCommandsURI, handler.mcpCollectionResource)
}

// mcpEntityResource reads one hearth://entity/... resource: Entity metadata and
// State, State history, Entity Events, Command history, or availability history.
func (handler *Handler) mcpEntityResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if resource.kind != mcpResourceKindEntity || len(resource.segments) == 0 {
		return nil, mcpResourceNotFoundError{}
	}
	entityID, err := devices.ParseEntityID(resource.segments[0])
	if err != nil {
		return nil, mcpResourceNotFoundError{}
	}
	switch strings.Join(resource.segments[1:], "/") {
	case "":
		return handler.mcpEntityStateResource(ctx, resource, entityID)
	case mcpResourceSuffixEvents:
		return handler.mcpEntityEventsResource(ctx, resource, entityID)
	case mcpResourceSuffixCommands:
		return handler.mcpEntityCommandsResource(ctx, resource, entityID)
	case mcpResourceSuffixStateHistory:
		return handler.mcpEntityStateHistoryResource(ctx, resource, entityID)
	case mcpResourceSuffixAvailability:
		return handler.mcpEntityAvailabilityResource(ctx, resource, entityID)
	default:
		return nil, mcpResourceNotFoundError{}
	}
}

func (handler *Handler) mcpEntityStateResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if queryErr := resource.query.Only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetEntity, &GetEntityInput{EntityID: string(entityID)},
		mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpEntityEventsResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListEntityEvents, &ListEntityEventsInput{
		EntityID: string(entityID), Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpEntityCommandsResource reads one page of an Entity's Command history,
// optionally filtered by Command status. Without a status filter it reads
// through ListEntityCommands, which owns the missing-Entity outcome. With one it
// first confirms the Entity exists through the service and then reads through
// ListCommands scoped to the Entity; the confirming read keeps the parent
// outcome independent of the status filter. A status the Command model rejects
// is refused before either read runs.
func (handler *Handler) mcpEntityCommandsResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.query.Only(mcpQueryStatus, mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	status, err := resource.query.Value(mcpQueryStatus)
	if err != nil {
		return nil, err
	}
	if status != "" && !devices.ValidCommandStatus(devices.CommandStatus(status)) {
		return nil, mcpapi.NewResourceInputError("invalid status")
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	if status != "" {
		if _, verifyErr := mcpRead(ctx, handler.GetEntity, &GetEntityInput{EntityID: string(entityID)},
			mcpFailureEntityNotFound); verifyErr != nil {
			return nil, verifyErr
		}
		output, readErr := mcpRead(ctx, handler.ListCommands, &ListCommandsInput{
			EntityID: string(entityID), Status: status, Limit: page.Limit, Cursor: page.Cursor,
		}, mcpFailureNone)
		if readErr != nil {
			return nil, readErr
		}
		return output.Body, nil
	}
	output, err := mcpRead(ctx, handler.ListEntityCommands, &ListEntityCommandsInput{
		EntityID: string(entityID), Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpEntityStateHistoryResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.query.Only(mcpQueryFilter, mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	filter, err := resource.query.Value(mcpQueryFilter)
	if err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListEntityStateHistory, &ListEntityStateHistoryInput{
		EntityID: string(entityID), Disposition: filter, Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpEntityAvailabilityResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListEntityAvailabilityHistory, &ListEntityAvailabilityHistoryInput{
		EntityID: string(entityID), Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpDeviceResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if resource.kind != mcpResourceKindDevice || len(resource.segments) != 1 {
		return nil, mcpResourceNotFoundError{}
	}
	deviceID, err := devices.ParseDeviceID(resource.segments[0])
	if err != nil {
		return nil, mcpResourceNotFoundError{}
	}
	if queryErr := resource.query.Only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetDevice, &GetDeviceInput{
		DeviceID: string(deviceID), EntityLimit: mcpapi.ResourcePageDefaultLimit,
	}, mcpFailureDeviceNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpAdapterResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if resource.kind != mcpResourceKindAdapter || len(resource.segments) == 0 {
		return nil, mcpResourceNotFoundError{}
	}
	adapterID := resource.segments[0]
	if !validAdapterID(adapterID) {
		return nil, mcpResourceNotFoundError{}
	}
	switch strings.Join(resource.segments[1:], "/") {
	case "":
		return handler.mcpAdapterStateResource(ctx, resource, adapterID)
	case mcpResourceSuffixHealth:
		return handler.mcpAdapterHealthResource(ctx, resource, adapterID)
	default:
		return nil, mcpResourceNotFoundError{}
	}
}

func (handler *Handler) mcpAdapterStateResource(
	ctx context.Context,
	resource mcpResourceURI,
	adapterID string,
) (any, error) {
	if queryErr := resource.query.Only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetAdapter, &GetAdapterInput{AdapterID: adapterID},
		mcpFailureAdapterNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpAdapterHealthResource(
	ctx context.Context,
	resource mcpResourceURI,
	adapterID string,
) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListAdapterHealthHistory, &ListAdapterHealthHistoryInput{
		AdapterID: adapterID, Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureAdapterNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpCommandResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if resource.kind != mcpResourceKindCommand || len(resource.segments) != 1 {
		return nil, mcpResourceNotFoundError{}
	}
	commandID, err := devices.ParseCommandID(resource.segments[0])
	if err != nil {
		return nil, mcpResourceNotFoundError{}
	}
	if queryErr := resource.query.Only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetCommand, &GetCommandInput{CommandID: string(commandID)},
		mcpFailureCommandNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpCollectionResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if len(resource.segments) != 0 {
		return nil, mcpResourceNotFoundError{}
	}
	switch resource.kind {
	case mcpResourceKindEntities:
		return handler.mcpEntitiesResource(ctx, resource)
	case mcpResourceKindDevices:
		return handler.mcpDevicesResource(ctx, resource)
	case mcpResourceKindAdapters:
		return handler.mcpAdaptersResource(ctx, resource)
	case mcpResourceKindCommands:
		return handler.mcpCommandsResource(ctx, resource)
	default:
		return nil, mcpResourceNotFoundError{}
	}
}

func (handler *Handler) mcpEntitiesResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.query.Only(mcpQueryDeviceID, mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	deviceID, err := resource.query.Value(mcpQueryDeviceID)
	if err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListEntities, &ListEntitiesInput{
		DeviceID: deviceID, Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpDevicesResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListDevices, &ListDevicesInput{
		Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpAdaptersResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListAdapters, &ListAdaptersInput{
		Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (handler *Handler) mcpCommandsResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.query.Only(
		mcpQueryEntityID, mcpQueryStatus, mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor,
	); err != nil {
		return nil, err
	}
	entityID, err := resource.query.Value(mcpQueryEntityID)
	if err != nil {
		return nil, err
	}
	status, err := resource.query.Value(mcpQueryStatus)
	if err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(ctx, handler.ListCommands, &ListCommandsInput{
		EntityID: entityID, Status: status, Limit: page.Limit, Cursor: page.Cursor,
	}, mcpFailureNone)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpResourceURI is one parsed hearth:// resource URI: its kind (the URI host),
// its decoded path segments, and its query parameters. The query is the shared
// mcpapi.ResourceQuery, so this package decides only which query parameters its
// own resources accept; rejection and page parsing cannot drift from the other
// modules that serve hearth:// resources.
type mcpResourceURI struct {
	kind     string
	segments []string
	query    mcpapi.ResourceQuery
}

// parseMCPResourceURI parses one hearth:// URI and rejects what this package
// cannot read: a foreign scheme, an empty kind, a fragment, a malformed escape,
// or a traversal path segment.
func parseMCPResourceURI(raw string) (mcpResourceURI, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return mcpResourceURI{}, mcpapi.NewResourceInputError("malformed resource URI")
	}
	if parsed.Scheme != mcpapi.ResourceScheme {
		return mcpResourceURI{}, mcpapi.NewResourceInputError(fmt.Sprintf(
			"resource URI must use the %s:// scheme", mcpapi.ResourceScheme,
		))
	}
	if parsed.Host == "" || parsed.Fragment != "" {
		return mcpResourceURI{}, mcpapi.NewResourceInputError("malformed resource URI")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return mcpResourceURI{}, mcpapi.NewResourceInputError("malformed resource URI query")
	}
	segments, err := mcpResourcePathSegments(parsed.Path)
	if err != nil {
		return mcpResourceURI{}, err
	}
	return mcpResourceURI{
		kind: parsed.Host, segments: segments, query: mcpapi.NewResourceQuery(query),
	}, nil
}

func mcpResourcePathSegments(path string) ([]string, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{}, nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, mcpapi.NewResourceInputError("malformed resource URI path")
		}
	}
	return segments, nil
}

// mcpResourceNotFoundError reports a resource URI this server does not serve.
// Read dispatchers return it so one place translates it to the SDK's
// resource-not-found error.
type mcpResourceNotFoundError struct{}

func (mcpResourceNotFoundError) Error() string {
	return "resource not found"
}

// mcpResourceFailure translates one failed resource read into the JSON-RPC error
// a client sees: a missing parent becomes resource-not-found, unreadable input
// becomes invalid params, and anything else stays internal without leaking
// detail.
func mcpResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*mcpapi.ResourceInputError](err); ok {
		return mcpapi.InvalidParamsError(inputError.Message)
	}
	if _, ok := errors.AsType[mcpResourceNotFoundError](err); ok {
		return mcp.ResourceNotFoundError(uri)
	}
	if toolError, ok := errors.AsType[*mcpapi.ToolError](err); ok {
		switch toolError.Code {
		case string(mcpFailureEntityNotFound), string(mcpFailureDeviceNotFound),
			string(mcpFailureAdapterNotFound), string(mcpFailureCommandNotFound):
			return mcp.ResourceNotFoundError(uri)
		case string(mcpFailureInvalidRequest):
			return mcpapi.InvalidParamsError(toolError.Message)
		}
	}
	return mcpResourceInternalError()
}

func mcpResourceInternalError() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}
