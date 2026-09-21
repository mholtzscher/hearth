package mcpapi

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// The hearth:// namespace is served by more than one module, so the parts of a
// resource URI that cannot differ between them live here: the scheme, the JSON
// body MIME type, the page parameters, the page bounds, and the two query
// rejections every resource reader applies. Each module still owns the hosts,
// paths, and query names specific to its own resource catalog.
const (
	// ResourceScheme is the URI scheme every Hearth MCP resource address uses.
	ResourceScheme = "hearth"

	// ResourceMIMEType is the MIME type of every Hearth MCP resource body.
	ResourceMIMEType = "application/json"

	// ResourceQueryLimit and ResourceQueryCursor are the query parameters every
	// paginated resource accepts.
	ResourceQueryLimit  = "limit"
	ResourceQueryCursor = "cursor"

	// ResourcePageDefaultLimit is the page size a resource read uses when a
	// client omits limit; it matches the Huma default for an omitted limit.
	ResourcePageDefaultLimit = 50

	// ResourcePageMinimumLimit and ResourcePageMaximumLimit bound an explicit
	// limit; they match the Huma page size range.
	ResourcePageMinimumLimit = 1
	ResourcePageMaximumLimit = 200
)

// ResourceQuery is the decoded query of one resource URI. It owns the two
// rejections every resource reader shares — a parameter the resource does not
// accept, and a parameter that appears more than once — so no module reads the
// same query differently.
type ResourceQuery url.Values

// NewResourceQuery wraps the query of one resource URI, as decoded by
// [url.ParseQuery], as the shared resource query.
func NewResourceQuery(values url.Values) ResourceQuery {
	return ResourceQuery(values)
}

// Get returns one query parameter's first value, matching [url.Values.Get], or
// "" when the parameter is absent. A reader that resolves one value should call
// [ResourceQuery.Single] instead, which also rejects a repeated parameter.
func (query ResourceQuery) Get(key string) string {
	return url.Values(query).Get(key)
}

// Only rejects a query parameter this resource does not accept; silently
// ignoring one would return a broader page than a client asked for.
func (query ResourceQuery) Only(allowed ...string) error {
	for key := range query {
		if !slices.Contains(allowed, key) {
			return NewResourceInputError(fmt.Sprintf("unsupported query parameter %q", key))
		}
	}
	return nil
}

// Single returns one optional query parameter's value and whether it was
// present; a repeated parameter is rejected rather than resolved arbitrarily.
func (query ResourceQuery) Single(key string) (string, bool, error) {
	values, present := query[key]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, NewResourceInputError(fmt.Sprintf("query parameter %q must appear once", key))
	}
	return values[0], true, nil
}

// Value returns one optional query parameter's value, or "" when it was absent.
func (query ResourceQuery) Value(key string) (string, error) {
	value, _, err := query.Single(key)
	return value, err
}

// Limit returns the page size of one resource read: the default when the client
// omitted limit, and a bounded explicit value otherwise.
func (query ResourceQuery) Limit() (int, error) {
	raw, present, err := query.Single(ResourceQueryLimit)
	if err != nil {
		return 0, err
	}
	if !present {
		return ResourcePageDefaultLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, NewResourceInputError("limit must be an integer")
	}
	if value < ResourcePageMinimumLimit || value > ResourcePageMaximumLimit {
		return 0, NewResourceInputError(fmt.Sprintf(
			"limit must be between %d and %d", ResourcePageMinimumLimit, ResourcePageMaximumLimit,
		))
	}
	return value, nil
}

// Cursor returns the opaque cursor of one resource read, or "" when the client
// omitted it. MCP defines no cursor format: the value is forwarded to the
// service uninterpreted, exactly as a Huma handler forwards it.
func (query ResourceQuery) Cursor() (string, error) {
	return query.Value(ResourceQueryCursor)
}

// Page parses the page size and opaque cursor every paginated resource accepts.
// The limit is resolved before the cursor, so a client that sent both a bad
// limit and a repeated cursor learns about the limit.
func (query ResourceQuery) Page() (ResourcePage, error) {
	limit, err := query.Limit()
	if err != nil {
		return ResourcePage{}, err
	}
	cursor, err := query.Cursor()
	if err != nil {
		return ResourcePage{}, err
	}
	return ResourcePage{Limit: limit, Cursor: cursor}, nil
}

// ResourcePage is the parsed cursor and page size of one paginated resource
// read.
type ResourcePage struct {
	Limit  int
	Cursor string
}

// ResourceInputError is an unreadable resource URI or query; a resource's
// failure mapper translates it with [InvalidParamsError], mirroring the Huma 400
// for a malformed request.
type ResourceInputError struct {
	// Message is the client-visible reason the request was rejected.
	Message string
}

// NewResourceInputError reports one unreadable resource request.
func NewResourceInputError(message string) *ResourceInputError {
	return &ResourceInputError{Message: message}
}

func (err *ResourceInputError) Error() string {
	return err.Message
}

// InvalidParamsError renders one unreadable resource request as the JSON-RPC
// invalid-params error a client sees.
func InvalidParamsError(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}
