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
)

const (
	mcpResourceKindAdapter      = "adapter"
	mcpResourceKindAdapters     = "adapters"
	mcpResourceSuffixHealth     = "health/history"
	mcpAdapterURITemplate       = "hearth://adapter/{adapter_id}"
	mcpAdapterHealthURITemplate = "hearth://adapter/{adapter_id}/health/history{?cursor,limit}"
	mcpAdaptersURITemplate      = "hearth://adapters{?cursor,limit}"
	mcpAdaptersURI              = "hearth://adapters"
)

func (handler *Handler) registerResources(server *mcpapi.Server) {
	register := func(name, description, uriTemplate string, read mcpapi.ResourceRead) {
		mcpapi.RegisterResourceTemplate(
			server,
			name,
			description,
			uriTemplate,
			mcpapi.ResourceMIMEType,
			read,
			mcpResourceFailure,
		)
	}
	register("adapter", "One Adapter and its current health", mcpAdapterURITemplate, handler.mcpAdapterResource)
	register(
		"adapter_health_history",
		"One Adapter's health history page",
		mcpAdapterHealthURITemplate,
		handler.mcpAdapterResource,
	)
	register(
		"adapters",
		"One page of Adapters and their current health",
		mcpAdaptersURITemplate,
		handler.mcpCollectionResource,
	)
	mcpapi.RegisterResource(
		server,
		"adapters",
		"One page of Adapters and their current health",
		mcpAdaptersURI,
		mcpapi.ResourceMIMEType,
		handler.mcpCollectionResource,
		mcpResourceFailure,
	)
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
	output, err := mcpRead(ctx, handler.GetAdapter, &GetAdapterInput{AdapterID: adapterID}, mcpFailureAdapterNotFound)
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
	output, err := mcpRead(
		ctx,
		handler.ListAdapterHealthHistory,
		&ListAdapterHealthHistoryInput{AdapterID: adapterID, Limit: page.Limit, Cursor: page.Cursor},
		mcpFailureAdapterNotFound,
	)
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
	if resource.kind != mcpResourceKindAdapters || len(resource.segments) != 0 {
		return nil, mcpResourceNotFoundError{}
	}
	return handler.mcpAdaptersResource(ctx, resource)
}

func (handler *Handler) mcpAdaptersResource(ctx context.Context, resource mcpResourceURI) (any, error) {
	if err := resource.query.Only(mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	output, err := mcpRead(
		ctx,
		handler.ListAdapters,
		&ListAdaptersInput{Limit: page.Limit, Cursor: page.Cursor},
		mcpFailureNone,
	)
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

type mcpResourceURI struct {
	kind     string
	segments []string
	query    mcpapi.ResourceQuery
}

func parseMCPResourceURI(raw string) (mcpResourceURI, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return mcpResourceURI{}, mcpapi.NewResourceInputError("malformed resource URI")
	}
	if parsed.Scheme != mcpapi.ResourceScheme {
		return mcpResourceURI{}, mcpapi.NewResourceInputError(
			fmt.Sprintf("resource URI must use the %s:// scheme", mcpapi.ResourceScheme),
		)
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
	return mcpResourceURI{kind: parsed.Host, segments: segments, query: mcpapi.NewResourceQuery(query)}, nil
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

type mcpResourceNotFoundError struct{}

func (mcpResourceNotFoundError) Error() string { return "resource not found" }

func mcpResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*mcpapi.ResourceInputError](err); ok {
		return mcpapi.InvalidParamsError(inputError.Message)
	}
	if _, ok := errors.AsType[mcpResourceNotFoundError](err); ok {
		return mcp.ResourceNotFoundError(uri)
	}
	if toolError, ok := errors.AsType[*mcpapi.ToolError](err); ok {
		switch toolError.Code {
		case string(mcpFailureAdapterNotFound):
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
