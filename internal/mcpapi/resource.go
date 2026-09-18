package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResourceRead reads one resource URI into the body a client attaches as
// context. Modules implement it over the same service methods and bodies as
// their Huma GET handlers, so a resource returns the exact JSON body the
// matching route returns.
type ResourceRead func(context.Context, string) (any, error)

// ResourceFailure maps one failed resource read onto the client-visible error:
// a missing parent becomes resource-not-found, unreadable input becomes
// invalid params, and anything else stays a generic internal error so no
// server detail leaks.
type ResourceFailure func(uri string, err error) error

// RegisterResource publishes one parameterless collection URI as a concrete
// resource. Templates alone leave resources/list empty, and clients that
// materialize resources as tools only see concrete resources, so the
// parameterless collections are served both ways: the bare URI reads the
// default first page here, and paged reads keep flowing through the template.
// Parameterized families stay template-only because their URIs cannot name one
// concrete resource.
func RegisterResource(
	server *Server,
	name string,
	description string,
	uri string,
	mimeType string,
	read ResourceRead,
	failure ResourceFailure,
) {
	server.Raw().AddResource(
		&mcp.Resource{URI: uri, Name: name, Description: description, MIMEType: mimeType},
		resourceHandler(mimeType, read, failure),
	)
}

// RegisterResourceTemplate publishes one parameterized URI family as a
// resource template whose read dispatches on the requested URI.
func RegisterResourceTemplate(
	server *Server,
	name string,
	description string,
	uriTemplate string,
	mimeType string,
	read ResourceRead,
	failure ResourceFailure,
) {
	server.Raw().AddResourceTemplate(
		&mcp.ResourceTemplate{
			Name: name, Description: description, URITemplate: uriTemplate, MIMEType: mimeType,
		},
		resourceHandler(mimeType, read, failure),
	)
}

// resourceHandler adapts a URI-keyed body reader onto the SDK's resource
// handler contract. A reader returns domain failures for the failure mapper;
// an SDK protocol error (such as resource-not-found) crosses unchanged, so
// the mapper only ever sees failures it models.
func resourceHandler(
	mimeType string,
	read ResourceRead,
	failure ResourceFailure,
) func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		body, err := read(ctx, request.Params.URI)
		if err != nil {
			if _, ok := errors.AsType[*jsonrpc.Error](err); ok {
				return nil, err
			}
			return nil, failure(request.Params.URI, err)
		}
		return ResourceResult(request.Params.URI, mimeType, body)
	}
}

// ResourceResult encodes one read body as the JSON text a client attaches as
// context. The mimeType rides the content beside the URI; a body that cannot
// be marshalled is a generic internal error, never client-visible detail.
func ResourceResult(uri string, mimeType string, body any) (*mcp.ReadResourceResult, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: mimeType, Text: string(encoded),
	}}}, nil
}
