// Package mcpecho mounts an mcpapi Server onto an Echo router as one route.
//
// Echo keeps ownership of routing, logging, timeouts, and all HTTP middleware;
// the MCP SDK keeps ownership of protocol handling. This package is the seam
// between them.
package mcpecho

import (
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// Mount serves server's MCP endpoint at path on e.
//
// The optional middleware wraps only the MCP route. Echo middleware that places
// request-scoped values into the request context, by deriving a context and
// calling [Context.SetRequest], reaches MCP tool handlers because both
// transports share the standard context mechanism.
func Mount(e *echo.Echo, path string, server *mcpapi.Server, middleware ...echo.MiddlewareFunc) {
	e.Any(path, echo.WrapHandler(server.HTTPHandler()), middleware...)
}
