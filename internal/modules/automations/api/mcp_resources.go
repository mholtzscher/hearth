package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automation resource addresses use the reserved hearth:// scheme
// (mcpapi.ResourceScheme) and JSON bodies (mcpapi.ResourceMIMEType),
// independent of the HTTP paths that back the same reads.
const (
	automationResourceHost = "automation"
	// automationsCollectionResourceHost addresses the parameterless
	// collection; every other automation address lives under "automation".
	automationsCollectionResourceHost = "automations"
)

// automationResourceTemplates name the read-only automation resources the MCP
// server publishes. Every read calls the same Huma GET as its tool, so the body
// a resource returns is the route's body verbatim. The collection is served both
// as a template and as a concrete resource: the bare URI reads the default first
// page (which resource-materializing clients surface as a tool), while paged
// reads flow through the template.
const (
	automationDefinitionResourceTemplate  = "hearth://automation/{automation_id}"
	automationHistoryResourceTemplate     = "hearth://automation/{automation_id}/history{?cursor,limit}"
	automationsCollectionResourceTemplate = "hearth://automations{?cursor,limit}"
	automationsCollectionResourceURI      = "hearth://automations"
)

// automationResourceAddress is one parsed hearth:// automation resource URI. Its
// query is the shared mcpapi.ResourceQuery, which owns the query parameters an
// automation history resource accepts: mcpapi.ResourceQueryCursor and
// mcpapi.ResourceQueryLimit.
type automationResourceAddress struct {
	// AutomationID is the raw path segment; it is validated before any read.
	AutomationID string
	// History reports whether the URI addresses retained history rather than the
	// current definition.
	History bool
	// Query carries the decoded query parameters for a history address.
	Query mcpapi.ResourceQuery
}

func (handler *Handler) registerResources(server *mcpapi.Server) {
	mcpapi.RegisterResourceTemplate(server,
		"automation",
		"One current Automation definition with its revision and timestamps",
		automationDefinitionResourceTemplate, mcpapi.ResourceMIMEType,
		handler.automationDefinitionBody, automationResourceFailure)
	mcpapi.RegisterResourceTemplate(server,
		"automation_history",
		"Newest-first retained Run and Skip history for one Automation",
		automationHistoryResourceTemplate, mcpapi.ResourceMIMEType,
		handler.automationHistoryBody, automationResourceFailure)
	mcpapi.RegisterResourceTemplate(server,
		"automations",
		"Automations, one page",
		automationsCollectionResourceTemplate, mcpapi.ResourceMIMEType,
		handler.automationsCollectionBody, automationResourceFailure)
	mcpapi.RegisterResource(server,
		"automations",
		"Automations, one page",
		automationsCollectionResourceURI, mcpapi.ResourceMIMEType,
		handler.automationsCollectionBody, automationResourceFailure)
}

func (handler *Handler) automationDefinitionBody(ctx context.Context, uri string) (any, error) {
	address, err := parseAutomationResourceAddress(uri)
	if err != nil {
		if _, ok := errors.AsType[*mcpapi.ResourceInputError](err); ok {
			return nil, automationResourceFailure(uri, err)
		}
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if address.History {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	// The SDK routes a resource only to a template its URI matches, and this
	// template declares no query, so a query parameter is rejected before this
	// reader runs. Only still guards the shape in case that changes.
	if queryErr := address.Query.Only(); queryErr != nil {
		return nil, automationResourceFailure(uri, queryErr)
	}
	id, err := automations.ParseAutomationID(address.AutomationID)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	output, err := handler.GetAutomation(ctx, &GetAutomationInput{AutomationID: string(id)})
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	return output.Body, nil
}

// automationHistoryBody reads one newest-first history page through the same
// Huma read the history route and the list_automation_history tool use, so the
// page default, cursor decode, and next-cursor encoding cannot drift. The
// returned body is the Huma body verbatim: a resource has no derived output
// schema, so the raw-JSON leaves the tool retypes marshal here unchanged.
func (handler *Handler) automationHistoryBody(ctx context.Context, uri string) (any, error) {
	address, err := parseAutomationResourceAddress(uri)
	if err != nil {
		if _, ok := errors.AsType[*mcpapi.ResourceInputError](err); ok {
			return nil, automationResourceFailure(uri, err)
		}
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if !address.History {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if queryErr := address.Query.Only(mcpapi.ResourceQueryCursor, mcpapi.ResourceQueryLimit); queryErr != nil {
		return nil, automationResourceFailure(uri, queryErr)
	}
	id, err := automations.ParseAutomationID(address.AutomationID)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	page, err := address.Query.Page()
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	output, err := handler.ListHistory(ctx, &ListHistoryInput{
		AutomationID: string(id), Limit: page.Limit, Cursor: page.Cursor,
	})
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	return output.Body, nil
}

// automationsCollectionBody reads one ID-ascending page of Automations through
// the same Huma read the collection route and the list_automations tool use.
func (handler *Handler) automationsCollectionBody(ctx context.Context, uri string) (any, error) {
	address, err := parseAutomationsCollectionAddress(uri)
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	if queryErr := address.Query.Only(mcpapi.ResourceQueryCursor, mcpapi.ResourceQueryLimit); queryErr != nil {
		return nil, automationResourceFailure(uri, queryErr)
	}
	page, err := address.Query.Page()
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	output, err := handler.ListAutomations(ctx, &ListAutomationsInput{
		Limit: page.Limit, Cursor: page.Cursor,
	})
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	return output.Body, nil
}

// parseAutomationsCollectionAddress splits the parameterless collection URI into
// its query, rejecting any other scheme, host, or path shape.
func parseAutomationsCollectionAddress(uri string) (automationResourceAddress, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return automationResourceAddress{}, fmt.Errorf("parse automations collection URI: %w", err)
	}
	if parsed.Scheme != mcpapi.ResourceScheme || parsed.Host != automationsCollectionResourceHost ||
		parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return automationResourceAddress{}, errors.New("automations collection URI has an unsupported shape")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return automationResourceAddress{}, mcpapi.NewResourceInputError(
			"automation resource URI has a malformed query",
		)
	}
	return automationResourceAddress{Query: mcpapi.NewResourceQuery(query)}, nil
}

// parseAutomationResourceAddress splits one hearth:// automation URI into its
// addressable parts, rejecting any other scheme, host, path shape, or query.
func parseAutomationResourceAddress(uri string) (automationResourceAddress, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return automationResourceAddress{}, fmt.Errorf("parse automation resource URI: %w", err)
	}
	if parsed.Scheme != mcpapi.ResourceScheme || parsed.Host != automationResourceHost ||
		parsed.Fragment != "" {
		return automationResourceAddress{}, errors.New("automation resource URI has an unsupported scheme or host")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return automationResourceAddress{}, mcpapi.NewResourceInputError(
			"automation resource URI has a malformed query",
		)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(segments) == 1 && segments[0] != "":
		return automationResourceAddress{
			AutomationID: segments[0], Query: mcpapi.NewResourceQuery(query),
		}, nil
	case len(segments) == 2 && segments[0] != "" && segments[1] == "history":
		return automationResourceAddress{
			AutomationID: segments[0], History: true, Query: mcpapi.NewResourceQuery(query),
		}, nil
	default:
		return automationResourceAddress{}, errors.New("automation resource URI has an unsupported path")
	}
}

// automationResourceFailure translates one failed resource read into the
// JSON-RPC error a client sees: an unreadable request becomes invalid params, a
// missing Automation or history becomes resource-not-found, and every other
// failure stays a generic internal error so no server detail leaks.
//
// The readers call the shared Huma operations, so the failures that reach this
// mapper are mostly Huma problems: a 400 is the input this server rejected and a
// 404 is the missing parent. The raw domain sentinels stay handled too, because
// a reader may add one without going through Huma.
func automationResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*mcpapi.ResourceInputError](err); ok {
		return mcpapi.InvalidParamsError(inputError.Message)
	}
	if problem, ok := errors.AsType[*automationProblemError](err); ok {
		switch problem.Status {
		case http.StatusBadRequest:
			return mcpapi.InvalidParamsError(problem.Detail)
		case http.StatusNotFound:
			return mcp.ResourceNotFoundError(uri)
		}
		return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
	}
	if errors.Is(err, automations.ErrAutomationNotFound) || errors.Is(err, automations.ErrHistoryNotFound) {
		return mcp.ResourceNotFoundError(uri)
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}
