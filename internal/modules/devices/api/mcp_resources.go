package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The hearth:// namespace is read-only and poll-only: one template per catalog
// entry, each reading through the same Huma handler the matching tool uses, so a
// resource returns the exact JSON body the matching Huma GET returns.
const (
	mcpResourceScheme   = "hearth"
	mcpResourceMIMEType = "application/json"

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

	// mcpQuery* are the query parameters the resource templates accept.
	mcpQueryLimit    = "limit"
	mcpQueryCursor   = "cursor"
	mcpQueryFilter   = "filter"
	mcpQueryStatus   = "status"
	mcpQueryEntityID = "entity_id"
	mcpQueryDeviceID = "device_id"
)

// Resource templates as the MCP resource catalog lists them.
//
// Entity Command history accepts the catalog's `status` filter. ListEntityCommands
// itself takes no status filter, so the unfiltered read stays on it while a
// status-filtered read routes to the household ListCommands read, which already
// scopes by Entity and status with the same ordering, page size, and opaque
// cursor scheme. Widening the shared read interface instead would touch the
// domain, repository, SQL, Huma route, and cursor for one resource filter.
//
// ListCommands scopes by Entity but never reports an unknown parent, so the
// status-filtered read verifies the Entity first; see mcpEntityCommandsResource.
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
// concrete resources alongside their templates. Templates alone leave
// resources/list empty, and clients that materialize resources (such as the Pi
// MCP adapter's read_* tools) only see concrete resources. The four
// collections take no required parameters, so their bare URIs are stable
// addresses for the default first page; paged reads keep flowing through the
// templates. The SDK routes a bare URI to the concrete resource first, and
// both entries dispatch to the same collection reader, so bodies are
// identical. Parameterized families stay template-only: their URIs cannot name
// one concrete resource.
const (
	mcpAdaptersURI = "hearth://adapters"
	mcpEntitiesURI = "hearth://entities"
	mcpDevicesURI  = "hearth://devices"
	mcpCommandsURI = "hearth://commands"
)

// registerResources registers the devices-owned hearth:// resources.
func (handler *Handler) registerResources(server *mcpapi.Server) {
	register := func(name, description, uriTemplate string, read mcpapi.ResourceRead) {
		mcpapi.RegisterResourceTemplate(server, name, description, uriTemplate,
			mcpResourceMIMEType, read, mcpResourceFailure)
	}
	registerConcrete := func(name, description, uri string, read mcpapi.ResourceRead) {
		mcpapi.RegisterResource(server, name, description, uri,
			mcpResourceMIMEType, read, mcpResourceFailure)
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

// mcpEntityStateResource reads one Entity's metadata and current State.
func (handler *Handler) mcpEntityStateResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if queryErr := resource.only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetEntity, &GetEntityInput{EntityID: string(entityID)},
		mcpFailureEntityNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpEntityEventsResource reads one page of an Entity's Entity Event history.
func (handler *Handler) mcpEntityEventsResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.only(mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.page()
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
// optionally filtered by Command status.
//
// Without a status filter it reads through ListEntityCommands, which owns the
// missing-Entity outcome. With one it first confirms the Entity exists through
// the service and then reads through ListCommands scoped to the Entity, because
// ListEntityCommands has no status filter. ListCommands itself cannot report an
// unknown parent, so without the confirming read a filtered read of a missing
// Entity would return an empty page instead of the not-found the unfiltered
// read returns; the parent outcome stays independent of the status filter. A
// status the Command model rejects is unreadable input, refused before either
// read runs.
func (handler *Handler) mcpEntityCommandsResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.only(mcpQueryStatus, mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	status, err := resource.value(mcpQueryStatus)
	if err != nil {
		return nil, err
	}
	if status != "" && !devices.ValidCommandStatus(devices.CommandStatus(status)) {
		return nil, &mcpResourceInputError{message: "invalid status"}
	}
	page, err := resource.page()
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

// mcpEntityStateHistoryResource reads one page of an Entity's retained State
// history. The filter query parameter maps onto the Huma disposition parameter.
func (handler *Handler) mcpEntityStateHistoryResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.only(mcpQueryFilter, mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	filter, err := resource.value(mcpQueryFilter)
	if err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpEntityAvailabilityResource reads one page of an Entity's availability
// history.
func (handler *Handler) mcpEntityAvailabilityResource(
	ctx context.Context,
	resource mcpResourceURI,
	entityID devices.EntityID,
) (any, error) {
	if err := resource.only(mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpDeviceResource reads one Device and the first page of its Entities.
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
	if queryErr := resource.only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetDevice, &GetDeviceInput{
		DeviceID: string(deviceID), EntityLimit: mcpDefaultPageLimit,
	}, mcpFailureDeviceNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpAdapterResource reads one Adapter's health or one page of its health
// history.
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

// mcpAdapterStateResource reads one Adapter's health and runtime evidence.
func (handler *Handler) mcpAdapterStateResource(
	ctx context.Context,
	resource mcpResourceURI,
	adapterID string,
) (any, error) {
	if queryErr := resource.only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetAdapter, &GetAdapterInput{AdapterID: adapterID},
		mcpFailureAdapterNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpAdapterHealthResource reads one page of an Adapter's health history.
func (handler *Handler) mcpAdapterHealthResource(
	ctx context.Context,
	resource mcpResourceURI,
	adapterID string,
) (any, error) {
	if err := resource.only(mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpCommandResource reads one Command record.
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
	if queryErr := resource.only(); queryErr != nil {
		return nil, queryErr
	}
	output, err := mcpRead(ctx, handler.GetCommand, &GetCommandInput{CommandID: string(commandID)},
		mcpFailureCommandNotFound)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// mcpCollectionResource reads one page of a household collection.
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

// mcpEntitiesResource reads one page of Entities, optionally scoped to a Device.
func (handler *Handler) mcpEntitiesResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.only(mcpQueryDeviceID, mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	deviceID, err := resource.value(mcpQueryDeviceID)
	if err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpDevicesResource reads one page of Devices.
func (handler *Handler) mcpDevicesResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.only(mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpAdaptersResource reads one page of Adapters.
func (handler *Handler) mcpAdaptersResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.only(mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.page()
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

// mcpCommandsResource reads one page of household Command history, optionally
// filtered by Entity and status.
func (handler *Handler) mcpCommandsResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.only(mcpQueryEntityID, mcpQueryStatus, mcpQueryLimit, mcpQueryCursor); err != nil {
		return nil, err
	}
	entityID, err := resource.value(mcpQueryEntityID)
	if err != nil {
		return nil, err
	}
	status, err := resource.value(mcpQueryStatus)
	if err != nil {
		return nil, err
	}
	page, err := resource.page()
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
// its decoded path segments, and its query parameters.
type mcpResourceURI struct {
	kind     string
	segments []string
	query    url.Values
}

// parseMCPResourceURI parses one hearth:// URI and rejects what this package
// cannot read: a foreign scheme, an empty kind, a fragment, a malformed escape,
// or a traversal path segment.
func parseMCPResourceURI(raw string) (mcpResourceURI, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return mcpResourceURI{}, &mcpResourceInputError{message: "malformed resource URI"}
	}
	if parsed.Scheme != mcpResourceScheme {
		return mcpResourceURI{}, &mcpResourceInputError{
			message: fmt.Sprintf("resource URI must use the %s:// scheme", mcpResourceScheme),
		}
	}
	if parsed.Host == "" || parsed.Fragment != "" {
		return mcpResourceURI{}, &mcpResourceInputError{message: "malformed resource URI"}
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return mcpResourceURI{}, &mcpResourceInputError{message: "malformed resource URI query"}
	}
	segments, err := mcpResourcePathSegments(parsed.Path)
	if err != nil {
		return mcpResourceURI{}, err
	}
	return mcpResourceURI{kind: parsed.Host, segments: segments, query: query}, nil
}

// mcpResourcePathSegments splits one resource URI path, rejecting empty and
// traversal segments so only literal path shapes match.
func mcpResourcePathSegments(path string) ([]string, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{}, nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, &mcpResourceInputError{message: "malformed resource URI path"}
		}
	}
	return segments, nil
}

// only rejects a query parameter this resource does not accept. Silently
// ignoring one would return a broader page than a client asked for.
func (uri mcpResourceURI) only(allowed ...string) error {
	for key := range uri.query {
		if !slices.Contains(allowed, key) {
			return &mcpResourceInputError{message: fmt.Sprintf("unsupported query parameter %q", key)}
		}
	}
	return nil
}

// single returns one optional query parameter's value and whether it was
// present. A repeated parameter is rejected rather than resolved arbitrarily.
func (uri mcpResourceURI) single(key string) (string, bool, error) {
	values, present := uri.query[key]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, &mcpResourceInputError{
			message: fmt.Sprintf("query parameter %q must appear once", key),
		}
	}
	return values[0], true, nil
}

// value returns one optional query parameter's value, or the empty string when
// the URI omits it.
func (uri mcpResourceURI) value(key string) (string, error) {
	value, _, err := uri.single(key)
	return value, err
}

// limit returns the page size this resource asks for, applying the default
// shared with the Huma query parameter.
func (uri mcpResourceURI) limit() (int, error) {
	value, present, err := uri.single(mcpQueryLimit)
	if err != nil {
		return 0, err
	}
	if !present {
		return mcpDefaultPageLimit, nil
	}
	parsed, parseErr := strconv.Atoi(value)
	if parseErr != nil {
		return 0, &mcpResourceInputError{message: "limit must be an integer"}
	}
	if _, rangeErr := mcpPageLimitValue(parsed); rangeErr != nil {
		return 0, &mcpResourceInputError{message: rangeErr.Error()}
	}
	return parsed, nil
}

// cursor returns the opaque cursor this resource asks for, uninterpreted.
func (uri mcpResourceURI) cursor() (string, error) {
	return uri.value(mcpQueryCursor)
}

// page returns the parsed cursor and page size of one paginated resource.
func (uri mcpResourceURI) page() (mcpPageQuery, error) {
	limit, err := uri.limit()
	if err != nil {
		return mcpPageQuery{}, err
	}
	cursor, err := uri.cursor()
	if err != nil {
		return mcpPageQuery{}, err
	}
	return mcpPageQuery{Limit: limit, Cursor: cursor}, nil
}

// mcpPageQuery is the parsed cursor and page size of one paginated resource.
type mcpPageQuery struct {
	Limit  int
	Cursor string
}

// mcpResourceInputError is an unreadable resource URI or query. It becomes a
// JSON-RPC invalid-params error, mirroring the Huma 400 for a malformed
// request.
type mcpResourceInputError struct {
	message string
}

// Error implements the error interface.
func (err *mcpResourceInputError) Error() string {
	return err.message
}

// mcpResourceNotFoundError reports a resource URI this server does not serve.
// Read dispatchers return it so one place translates it to the SDK's
// resource-not-found error.
type mcpResourceNotFoundError struct{}

// Error implements the error interface.
func (mcpResourceNotFoundError) Error() string {
	return "resource not found"
}

// mcpResourceFailure translates one failed resource read into the JSON-RPC error
// a client sees: a missing parent becomes resource-not-found, unreadable input
// becomes invalid params, and anything else stays internal without leaking
// detail.
func mcpResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*mcpResourceInputError](err); ok {
		return mcpInvalidParamsError(inputError.message)
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
			return mcpInvalidParamsError(toolError.Message)
		}
	}
	return mcpResourceInternalError()
}

// mcpInvalidParamsError reports one unreadable resource request with the
// JSON-RPC invalid-params code.
func mcpInvalidParamsError(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}

// mcpResourceInternalError reports a 500-class resource failure without leaking
// detail to the client.
func mcpResourceInternalError() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}
