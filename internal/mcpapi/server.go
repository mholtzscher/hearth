package mcpapi

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config identifies the MCP server reported to clients.
type Config struct {
	// Name is the programmatic implementation name.
	Name string
	// Version is the implementation version.
	Version string
	// Logger receives the structured diagnostics for failures the wrapper does
	// not model. Optional: a nil Logger selects [slog.Default].
	Logger *slog.Logger
}

// Server is a stateless MCP server served over Streamable HTTP.
//
// It wraps the official SDK server so callers register typed Tools instead of
// JSON-RPC handlers. Build one with [New], register tools with [Register], and
// expose it with [Server.HTTPHandler] or internal/mcpecho.
type Server struct {
	server  *mcp.Server
	handler http.Handler
}

// New builds a stateless MCP server identified by config, assembled with the
// wrapper's error mapping so a domain [ToolError] reaches clients as an isError
// result carrying both text and machine-readable fields.
func New(config Config) *Server {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    config.Name,
		Version: config.Version,
	}, nil)
	server.AddReceivingMiddleware(toolErrorMiddleware(logger))
	return &Server{
		server: server,
		handler: mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return server },
			&mcp.StreamableHTTPOptions{
				Stateless: true,
				// A dropped client stops the handler exactly as it stops the matching
				// REST handler. An admitted Command stays safe: the devices service
				// detaches its worker before waiting, so cancellation stops only the
				// wait and `get_command` still reads the durable record.
				PropagateRequestCancellation: true,
			},
		),
	}
}

// Raw returns the underlying SDK server for MCP features the wrapper does not
// cover, such as resources and prompts.
func (s *Server) Raw() *mcp.Server {
	return s.server
}

// HTTPHandler returns the Streamable HTTP handler for this server. It is
// stateless: every request carries its own full context and no session affinity
// is required, so overlapping calls are safe.
func (s *Server) HTTPHandler() http.Handler {
	return s.handler
}
