# Guide: Add MCP Support to an Existing Echo Go Service

The goal is to add MCP support to an existing Echo-based Go service without introducing another web framework or reimplementing MCP.

Use the official Go MCP SDK:

```text
github.com/modelcontextprotocol/go-sdk/mcp
```

Keep the architecture simple:

```text
                  Echo
                    │
         ┌──────────┴──────────┐
         │                     │
      REST API               /mcp
         │                     │
      Huma/etc.          official MCP SDK
         │                     │
         └─────────┬───────────┘
                   │
             application services
```

Echo should continue to own HTTP routing, auth, logging, tracing, CORS, rate limiting, etc.

The MCP SDK should own MCP protocol handling, Streamable HTTP, schemas, validation, JSON-RPC, and MCP sessions.

Add a thin local wrapper only to make tool registration ergonomic.

## 1. Create a small `mcpapi` package

Use something like:

```text
internal/
├── mcpapi/
│   ├── server.go
│   └── tool.go
│
└── mcpecho/
    └── echo.go
```

`mcpapi` should not depend on Echo.

It should wrap the official SDK.

Create a simple server:

```go
type Config struct {
    Name    string
    Version string
}

type Server struct {
    server *mcp.Server
}

func New(config Config) *Server {
    return &Server{
        server: mcp.NewServer(
            &mcp.Implementation{
                Name:    config.Name,
                Version: config.Version,
            },
            nil,
        ),
    }
}

func (s *Server) Raw() *mcp.Server {
    return s.server
}
```

Keep `Raw()` so unsupported MCP features can use the official SDK directly.

## 2. Add typed tool registration

The main ergonomic improvement should be a simplified handler:

```go
type Handler[I, O any] func(
    context.Context,
    I,
) (O, error)
```

Define a tool:

```go
type Tool[I, O any] struct {
    Name        string
    Description string

    Handler Handler[I, O]
}
```

Then implement:

```go
func Register[I, O any](
    server *Server,
    tool Tool[I, O],
) {
    mcp.AddTool(
        server.server,
        &mcp.Tool{
            Name:        tool.Name,
            Description: tool.Description,
        },
        func(
            ctx context.Context,
            req *mcp.CallToolRequest,
            input I,
        ) (*mcp.CallToolResult, O, error) {
            output, err := tool.Handler(ctx, input)

            return nil, output, err
        },
    )
}
```

The official SDK should remain responsible for:

```text
Go struct → JSON Schema
input validation
argument decoding
output validation
structured MCP output
protocol errors
```

Do not recreate those features.

## 3. Desired tool usage

Adding a tool should look roughly like this:

```go
type SearchInput struct {
    Query string `json:"query" jsonschema:"Search query"`
    Limit int    `json:"limit,omitempty" jsonschema:"Maximum results"`
}

type SearchOutput struct {
    Results []Result `json:"results"`
}

mcpapi.Register(
    mcpServer,
    mcpapi.Tool[SearchInput, SearchOutput]{
        Name:        "search",
        Description: "Search the catalog",

        Handler: func(
            ctx context.Context,
            input SearchInput,
        ) (SearchOutput, error) {
            results, err := catalog.Search(
                ctx,
                input.Query,
                input.Limit,
            )

            if err != nil {
                return SearchOutput{}, err
            }

            return SearchOutput{
                Results: results,
            }, nil
        },
    },
)
```

The handler should contain no JSON-RPC or MCP plumbing.

## 4. Expose the MCP HTTP handler

Add a method like:

```go
func (s *Server) HTTPHandler() http.Handler {
    return mcp.NewStreamableHTTPHandler(
        func(*http.Request) *mcp.Server {
            return s.server
        },
        &mcp.StreamableHTTPOptions{
            Stateless: true,
        },
    )
}
```

The important part is that this returns a normal:

```go
http.Handler
```

That makes Echo integration straightforward.

## 5. Mount it in Echo

Create a tiny adapter package:

```go
package mcpecho

func Mount(
    e *echo.Echo,
    path string,
    server *mcpapi.Server,
    middleware ...echo.MiddlewareFunc,
) {
    e.Any(
        path,
        echo.WrapHandler(server.HTTPHandler()),
        middleware...,
    )
}
```

Then application startup becomes:

```go
e := echo.New()

e.Use(
    middleware.Recover(),
    middleware.RequestLogger(),
)

mcpServer := mcpapi.New(mcpapi.Config{
    Name:    "my-service",
    Version: "1.0.0",
})

mcpapi.Register(mcpServer, searchTool)
mcpapi.Register(mcpServer, getItemTool)

mcpecho.Mount(
    e,
    "/mcp",
    mcpServer,
)

e.Start(":8080")
```

Echo remains the HTTP server.

## 6. Reuse existing Echo authentication

Do not create a second auth system just for MCP unless MCP OAuth is specifically required.

One important detail: authenticated user information should be stored in the request's standard Go context, not only in `echo.Context`.

Prefer:

```go
ctx := auth.WithUser(
    c.Request().Context(),
    user,
)

c.SetRequest(
    c.Request().WithContext(ctx),
)
```

Then MCP tools can do:

```go
user, ok := auth.UserFromContext(ctx)
```

This allows both REST handlers and MCP tools to share the same application services.

## 7. Keep application services transport-independent

Application code should look like:

```go
func (s *CatalogService) Search(
    ctx context.Context,
    query string,
    limit int,
) ([]Result, error)
```

Avoid passing:

```go
echo.Context
*mcp.CallToolRequest
```

into the service layer.

REST and MCP should just be different adapters around the same services.

## 8. Add advanced MCP access only when needed

Most tools should use:

```go
func(context.Context, Input) (Output, error)
```

If a tool eventually needs session/request-specific MCP information, add an optional advanced handler:

```go
type HandlerWithRequest[I, O any] func(
    context.Context,
    *mcp.CallToolRequest,
    I,
) (O, error)
```

Don't force every tool to know about MCP internals.

## 9. Keep HTTP middleware in Echo

Continue using Echo for:

```text
auth
request IDs
logging
OTEL HTTP spans
recovery
rate limiting
CORS
```

Don't create duplicate `mcpapi` versions of those.

If you eventually need tool-level metrics or tracing, add that separately.

A useful trace hierarchy would be:

```text
POST /mcp
  └─ MCP tool: search
      └─ CatalogService.Search
          └─ database query
```

## 10. Test the integration

At minimum, test:

- MCP server initializes through `/mcp`
- `tools/list` includes registered tools
- a typed tool can be called successfully
- invalid input is rejected
- Echo middleware runs for MCP requests
- values placed into `context.Context` by Echo reach the MCP handler

Prefer using the official MCP client in integration tests instead of manually constructing JSON-RPC requests.

## 11. Keep the wrapper intentionally small

The initial wrapper probably only needs:

```go
mcpapi.New(...)
mcpapi.Register(...)
server.Raw()
server.HTTPHandler()
```

Optionally add stdio:

```go
server.RunStdio(ctx)
```

Do not immediately wrap:

```text
resources
prompts
sampling
elicitation
OAuth
sessions
notifications
tasks
```

Use `server.Raw()` and the official SDK directly when those are needed.

The desired end state is:

```text
Echo
 ├─ /api/* → REST/Huma
 └─ /mcp   → official MCP SDK
                 │
              mcpapi
                 │
           application services
```

The wrapper should feel like a small Huma-style typed façade over the official MCP SDK, not a new MCP framework.
