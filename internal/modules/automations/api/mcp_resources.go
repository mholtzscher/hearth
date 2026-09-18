package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automation resource addresses use the reserved hearth:// scheme, independent
// of the HTTP paths that back the same reads.
const (
	automationResourceScheme = "hearth"
	automationResourceHost   = "automation"
	automationResourceMIME   = "application/json"
)

// automationResourceQuery* are the only query parameters an automation history
// resource accepts. The definition resource accepts none.
const (
	automationResourceQueryCursor = "cursor"
	automationResourceQueryLimit  = "limit"
)

// automationResourceTemplates name the read-only automation resources the MCP
// server publishes. Every read calls the same service method as its Huma GET.
const (
	automationDefinitionResourceTemplate = "hearth://automation/{automation_id}"
	automationHistoryResourceTemplate    = "hearth://automation/{automation_id}/history{?cursor,limit}"
)

// automationResourceAddress is one parsed hearth:// automation resource URI.
type automationResourceAddress struct {
	// AutomationID is the raw path segment; it is validated before any read.
	AutomationID string
	// History reports whether the URI addresses retained history rather than
	// the current definition.
	History bool
	// Query carries the decoded query parameters for a history address.
	Query url.Values
}

// registerResources publishes the automation resource templates through the
// official SDK, which the wrapper exposes via [mcpapi.Server.Raw].
func (handler *Handler) registerResources(server *mcpapi.Server) {
	raw := server.Raw()
	raw.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: automationDefinitionResourceTemplate,
		Name:        "automation",
		Description: "One current Automation definition with its revision and timestamps",
		MIMEType:    automationResourceMIME,
	}, handler.readAutomationResource)
	raw.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: automationHistoryResourceTemplate,
		Name:        "automation history",
		Description: "Newest-first retained Run and Skip history for one Automation",
		MIMEType:    automationResourceMIME,
	}, handler.readAutomationHistoryResource)
}

// readAutomationResource reads one current Automation definition.
func (handler *Handler) readAutomationResource(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	uri := request.Params.URI
	address, err := parseAutomationResourceAddress(uri)
	if err != nil || address.History {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	// The SDK routes a resource only to a template its URI matches, and this
	// template declares no query, so a query parameter is rejected before this
	// reader runs. only() still guards the shape in case that changes.
	if queryErr := address.only(); queryErr != nil {
		return nil, automationResourceFailure(uri, queryErr)
	}
	id, err := automations.ParseAutomationID(address.AutomationID)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	record, err := handler.automations.GetAutomation(ctx, id)
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	return newResourceResult(uri, automationBody(record))
}

// readAutomationHistoryResource reads one newest-first history page.
func (handler *Handler) readAutomationHistoryResource(
	ctx context.Context,
	request *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
	uri := request.Params.URI
	address, err := parseAutomationResourceAddress(uri)
	if err != nil || !address.History {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if queryErr := address.only(automationResourceQueryCursor, automationResourceQueryLimit); queryErr != nil {
		return nil, automationResourceFailure(uri, queryErr)
	}
	id, err := automations.ParseAutomationID(address.AutomationID)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	limit, err := address.pageLimit()
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	params := automations.ListHistoryParams{AutomationID: id, Limit: limit}
	cursor, present, err := address.single(automationResourceQueryCursor)
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	if present && cursor != "" {
		recordedAt, entryID, cursorErr := decodeHistoryCursor(cursor, id)
		if cursorErr != nil {
			return nil, automationResourceFailure(uri, &automationResourceInputError{
				message: "automation history cursor is invalid",
			})
		}
		params.BeforeRecordedAt = recordedAt
		params.BeforeID = entryID
	}
	page, err := handler.automations.ListHistory(ctx, params)
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	body := AutomationHistoryCollectionBody{
		Items: make([]AutomationHistorySummaryBody, len(page.Items)),
	}
	for index, summary := range page.Items {
		body.Items[index] = historySummaryBody(summary)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		nextCursor, cursorErr := encodeHistoryCursor(id, last.RecordedAt, last.ID)
		if cursorErr != nil {
			return nil, automationResourceFailure(uri, errors.New("automation history cursor cannot be encoded"))
		}
		body.NextCursor = &nextCursor
	}
	return newResourceResult(uri, body)
}

// parseAutomationResourceAddress splits one hearth:// automation URI into its
// addressable parts, rejecting any other scheme, host, path shape, or query.
func parseAutomationResourceAddress(uri string) (automationResourceAddress, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return automationResourceAddress{}, fmt.Errorf("parse automation resource URI: %w", err)
	}
	if parsed.Scheme != automationResourceScheme || parsed.Host != automationResourceHost ||
		parsed.Fragment != "" {
		return automationResourceAddress{}, errors.New("automation resource URI has an unsupported scheme or host")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return automationResourceAddress{}, errors.New("automation resource URI has a malformed query")
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(segments) == 1 && segments[0] != "":
		return automationResourceAddress{AutomationID: segments[0], Query: query}, nil
	case len(segments) == 2 && segments[0] != "" && segments[1] == "history":
		return automationResourceAddress{AutomationID: segments[0], History: true, Query: query}, nil
	default:
		return automationResourceAddress{}, errors.New("automation resource URI has an unsupported path")
	}
}

// only rejects a query parameter this resource does not accept. Silently
// ignoring one would return a broader page than a client asked for.
func (address automationResourceAddress) only(allowed ...string) error {
	for key := range address.Query {
		if !slices.Contains(allowed, key) {
			return &automationResourceInputError{
				message: fmt.Sprintf("unsupported query parameter %q", key),
			}
		}
	}
	return nil
}

// single returns one optional query parameter's value and whether it was
// present. A repeated parameter is rejected rather than resolved arbitrarily.
func (address automationResourceAddress) single(key string) (string, bool, error) {
	values, present := address.Query[key]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, &automationResourceInputError{
			message: fmt.Sprintf("query parameter %q must appear once", key),
		}
	}
	return values[0], true, nil
}

// pageLimit returns the page size this resource asks for, applying the same
// default and bound as the history tool.
func (address automationResourceAddress) pageLimit() (int, error) {
	raw, present, err := address.single(automationResourceQueryLimit)
	if err != nil {
		return 0, err
	}
	if !present {
		return mcpPageDefaultLimit, nil
	}
	value, convertErr := strconv.Atoi(raw)
	if convertErr != nil {
		return 0, &automationResourceInputError{message: "limit must be an integer"}
	}
	if value < 1 || value > mcpPageMaximumLimit {
		return 0, &automationResourceInputError{message: "limit must be between 1 and 200"}
	}
	return value, nil
}

// automationResourceInputError is an unreadable resource URI, query, or cursor.
// It becomes a JSON-RPC invalid-params error, mirroring the Huma 400 for a
// malformed request.
type automationResourceInputError struct {
	message string
}

// Error implements the error interface.
func (err *automationResourceInputError) Error() string {
	return err.message
}

// automationResourceFailure translates one failed resource read into the
// JSON-RPC error a client sees: an unreadable request becomes invalid params, a
// missing Automation or history becomes resource-not-found, and every other
// failure stays a generic internal error so no server detail leaks. The
// underlying error is never returned to the client.
func automationResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*automationResourceInputError](err); ok {
		return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: inputError.message}
	}
	if errors.Is(err, automations.ErrAutomationNotFound) || errors.Is(err, automations.ErrHistoryNotFound) {
		return mcp.ResourceNotFoundError(uri)
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}

// newResourceResult marshals one Huma read body into a JSON resource content.
func newResourceResult(uri string, payload any) (*mcp.ReadResourceResult, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, automationResourceFailure(uri, err)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: automationResourceMIME,
		Text:     string(encoded),
	}}}, nil
}
