# MCP as a first-class core interface

**Status:** Proposed; design grilled and confirmed 2026-09-17 (scope, transport, shape, trust, command semantics, naming, config, resources).
**Effort:** XL, split into eight ordered deliverables.
**Baseline:** `http_handler.go:NewHTTPHandler` owns Echo+Huma assembly; `run.go` serves one `http.Server`; `Devices` (15 methods) and `Automations` (8 methods) service interfaces back all Huma operations; no HTTP auth, middleware, CORS, or rate limiting; `github.com/modelcontextprotocol/go-sdk` is not a dependency.
**Guide:** `mcp-ida.md` (repo root, written without repo knowledge) supplies the transport pattern only; this spec binds it to Hearth's actual assembly and domain language. Where they conflict, this spec wins.

## 1. Problem statement

**Who:** An AI agent operating a Hearth household (and the technical self-hoster running it).

**What:** Core exposes its full capability surface over REST (`/v1` via Huma) but has no agent-native contract. Agents must hand-roll HTTP, pagination, polling, and error interpretation against an API designed for operators and dashboards.

**Why it matters:** Hearth's functional-replacement goal implies agents act on the household: read State, execute Commands, manage Automations. MCP (Streamable HTTP) is the standard agent contract, with typed tool schemas, addressable resources, and machine-readable errors. A first-class MCP interface means every Huma operation is reachable over MCP with equal semantics, maintained as a thin transport rather than a parallel API.

**Cost of not solving:** Every agent integration re-derives conventions (cursor handling, failure-code branching, deadline behavior) from REST, and they drift.

## 2. Non-goals for v1

- MCP prompts, sampling, elicitation, tasks, notifications beyond protocol defaults.
- MCP OAuth or any new auth system (trust model unchanged, §3.4).
- stdio transport; separate MCP port/process.
- Bridging NATS Device Facts to MCP resource subscriptions (poll-only, §6).
- New persistence, NATS subjects, or wire schemas; MCP reads existing services and stores nothing.

## 3. Constraints and decisions

### 3.1 Domain language (binding)

`CONTEXT.md` already owns *Operation* (a named command capability within an Entity type, e.g. `set`), *Command* (a durable request to change one entity), and *Step* (one Operation request in an Automation definition). To avoid collision:

- **MCP Tool** is transport-only vocabulary: a typed adapter exposing one Hearth query or action over MCP. It is never an Operation, Command, or Step.
- Tool names mirror Huma `operationId`s in snake_case (`execute_entity_command`), never bare Hearth operation names. The Hearth operation stays a parameter: `{"entity_id": "ent_…", "operation": "set", "parameters": {"value": true}}`.
- A future `CONTEXT.md` entry for MCP Tool is deliberately **not** proposed: the glossary stays free of transport vocabulary.

### 3.2 Transport: same Echo server at `/mcp`

- Echo continues to own HTTP routing, logging posture, timeouts, and process lifecycle. The official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk/mcp`) owns protocol handling: Streamable HTTP, JSON-RPC, schemas, validation, sessions.
- The MCP endpoint is `POST /mcp` (plus SDK-managed companion requests) on the existing `http_addr`, mounted with `e.Any("/mcp", echo.WrapHandler(...))` per `mcp-ida.md` §5.
- The handler runs **stateless** (`Stateless: true`): no session affinity. Each request carries full context; concurrent and overlapping Command calls are safe because Core already models overlapping Commands as independent.
- Server identity mirrors Huma: name `hearth`, version `1.0.0` (the `DefaultConfig("Hearth", "1.0.0")` string).

### 3.3 Pattern: thin `mcpapi` wrapper mimicking Huma registration

New package `internal/mcpapi` (no Echo dependency) wraps the official SDK:

```go
type Config struct {
    Name    string
    Version string
}

type Server struct {
    server *mcp.Server
}

func New(config Config) *Server

// Raw escapes to the official SDK for anything the wrapper does not cover
// (resources in D5, future prompts/sampling). No second framework is built.
func (s *Server) Raw() *mcp.Server

func (s *Server) HTTPHandler() http.Handler

type Handler[I, O any] func(context.Context, I) (O, error)

type Tool[I, O any] struct {
    Name        string
    Description string
    Handler     Handler[I, O]
}

func Register[I, O any](server *Server, tool Tool[I, O])

// Only for tools that genuinely need session/request metadata:
type HandlerWithRequest[I, O any] func(context.Context, *mcp.CallToolRequest, I) (O, error)
```

The SDK owns Go struct to JSON Schema derivation, input validation, argument decoding, output validation, and protocol errors. The wrapper owns registration ergonomics only, a Huma-style typed façade rather than a framework: call sites read like Huma's `Register(v1, devices)`, one `mcpapi.Register` per operation, handlers free of JSON-RPC plumbing. Only `internal/mcpapi` (and D5 resource code, via `Raw()`) imports SDK packages.

New package `internal/mcpecho` owns the single mount function:

```go
package mcpecho

func Mount(e *echo.Echo, path string, server *mcpapi.Server, middleware ...echo.MiddlewareFunc)
```

### 3.4 Trust: unchanged

MCP inherits the HTTP posture unchanged: no authentication, no MCP-specific CORS or rate-limiting code. The server binds loopback by default; non-loopback binding is an explicit trusted-network operator choice. Request-scoped values (request IDs today, auth if ever added) travel in the standard `context.Context`. Echo handlers must call `c.SetRequest(c.Request().WithContext(ctx))` for any value MCP tools may need, so both transports share one propagation mechanism (`mcp-ida.md` §6).

### 3.5 Services stay transport-independent

MCP handlers call the same `devicesapi.Devices` and `automationsapi.Automations` service interfaces as Huma handlers, reusing the same Huma input/output struct types where they already exist. No `echo.Context` or `*mcp.CallToolRequest` crosses the service boundary (except via the explicit `HandlerWithRequest` escape hatch, which must still translate to plain domain inputs before calling services).

## 4. Tool catalog (23 tools, full Huma parity)

Every Huma operation is callable as an MCP tool. With §3.5's shared I/O types, each tool handler is a mechanical translation from decoded input struct to service call.

### 4.1 Devices (15 tools, owner: `internal/modules/devices/api`)

| Tool (mirrors Huma `operationId`) | Huma operation | Service method |
|---|---|---|
| `list_entities` | `GET /v1/entities` | `ListEntities` |
| `get_entity` | `GET /v1/entities/{entity_id}` | `GetEntity` |
| `update_entity` | `PATCH /v1/entities/{entity_id}` | `SetEntityEnabled` |
| `execute_entity_command` | `POST /v1/entities/{entity_id}/commands` | `ExecuteCommand` |
| `list_entity_commands` | `GET /v1/entities/{entity_id}/commands` | `ListEntityCommands` |
| `list_devices` | `GET /v1/devices` | `ListDevices` |
| `get_device` | `GET /v1/devices/{device_id}` | `GetDevice` |
| `get_command` | `GET /v1/commands/{command_id}` | `GetCommand` |
| `list_commands` | `GET /v1/commands` | `ListCommands` |
| `list_adapters` | `GET /v1/adapters` | `ListAdapters` |
| `get_adapter` | `GET /v1/adapters/{adapter_id}` | `GetAdapter` |
| `list_adapter_health_history` | `GET /v1/adapters/{adapter_id}/health/history` | `ListAdapterHealthHistory` |
| `list_entity_availability_history` | `GET /v1/entities/{entity_id}/availability/history` | `ListEntityAvailabilityHistory` |
| `list_entity_state_history` | `GET /v1/entities/{entity_id}/state/history` | `ListEntityStateHistory` |
| `list_entity_events` | `GET /v1/entities/{entity_id}/events` | `ListEntityEvents` |

### 4.2 Automations (8 tools, owner: `internal/modules/automations/api`)

| Tool | Huma operation | Service method |
|---|---|---|
| `create_automation` | `POST /v1/automations` | `CreateAutomation` |
| `list_automations` | `GET /v1/automations` | `ListAutomations` |
| `get_automation` | `GET /v1/automations/{automation_id}` | `GetAutomation` |
| `replace_automation` | `PUT /v1/automations/{automation_id}` | `ReplaceAutomation` |
| `delete_automation` | `DELETE /v1/automations/{automation_id}` | `DeleteAutomation` |
| `start_automation_run` | `POST /v1/automations/{automation_id}/runs` | `StartManualRun` |
| `list_automation_history` | `GET /v1/automations/{automation_id}/history` | `ListHistory` |
| `get_automation_history_entry` | `GET /v1/automations/{automation_id}/history/{entry_id}` | `GetHistoryEntry` |

### 4.3 Command execution semantics (normative)

`execute_entity_command` **blocks** until the Command reaches its terminal outcome (`satisfied` or `dispatched`) or the operation-defined deadline fails it, identical to `POST /v1/entities/{entity_id}/commands`. Core commits the `requested` record before dispatch and proceeds even if the caller disconnects, so a blocked call that drops is still safe and its durable record stays readable via `get_command`. No async-submit/poll tool pair in v1.

### 4.4 Pagination (normative)

Huma's opaque endpoint-scoped keyset cursors pass through MCP as opaque strings (`cursor`, `next_cursor`, default limit 50, range 1-200). MCP defines no cursor format; handlers forward cursor strings to services uninterpreted, exactly as Huma handlers do.

## 5. Error mapping (normative, idiomatic MCP)

MCP distinguishes errors the model should recover from (tool results with `isError: true`) from errors it never sees (JSON-RPC protocol errors). Mapping:

| Hearth outcome | MCP representation |
|---|---|
| Domain failure with a stable code: `adapter_unhealthy`, `entity_disabled`, `entity_unavailable`, outcome timeout, upstream rejection (`failure_code` on the Command record), unknown entity/adapter/device (404) | Tool result with `isError: true` **and structured content preserving `failure_code`/`status`** (plus human-readable text). The agent can branch on codes instead of parsing prose. |
| Invalid tool input (bad ID format, out-of-range limit, schema violation) | SDK input-validation protocol error (automatic; handler never runs). |
| Internal failure (500-class, unpublished invariant) | Tool result `isError: true` with a generic message; full detail goes to structured server logs with safe payload metadata only, matching Observation-rejection logging posture. |

Problem-Details shapes (`application/problem+json`, including the 409 disabled-command schema) are translated to the above, not serialized verbatim: the `failure_code` string and a stable human summary cross the boundary; HTTP status codes do not (MCP has no status codes).

## 6. Resource catalog (all reads, `hearth://` scheme, poll-only)

Every Huma GET is additionally readable as an MCP resource so clients can attach household state as context. URIs use the `hearth://` scheme, independent of HTTP paths:

| Resource template | Backing read |
|---|---|
| `hearth://entity/{entity_id}` | `GetEntity` (metadata + current State, `state: null` when never observed) |
| `hearth://device/{device_id}` | `GetDevice` (detail + embedded entity page) |
| `hearth://adapter/{adapter_id}` | `GetAdapter` (health + runtime evidence) |
| `hearth://command/{command_id}` | `GetCommand` |
| `hearth://automation/{automation_id}` | `GetAutomation` |
| `hearth://entity/{entity_id}/state/history{?filter,cursor,limit}` | `ListEntityStateHistory` |
| `hearth://entity/{entity_id}/events{?cursor,limit}` | `ListEntityEvents` |
| `hearth://entity/{entity_id}/commands{?status,cursor,limit}` | `ListEntityCommands` |
| `hearth://entity/{entity_id}/availability/history{?cursor,limit}` | `ListEntityAvailabilityHistory` |
| `hearth://adapter/{adapter_id}/health/history{?cursor,limit}` | `ListAdapterHealthHistory` |
| `hearth://commands{?entity_id,status,cursor,limit}` | `ListCommands` |
| `hearth://entities{?device_id,cursor,limit}` | `ListEntities` |
| `hearth://devices{?cursor,limit}` | `ListDevices` |
| `hearth://adapters{?cursor,limit}` | `ListAdapters` |
| `hearth://automation/{automation_id}/history{?cursor,limit}` | `ListHistory` |
| `hearth://automations{?cursor,limit}` | `ListAutomations` |

Reads exist twice (tool and resource) by explicit decision. Both call the same service method with the same types, so the duplication stays transport-thin; §9 records the drift risk and A8 is the equivalence test. Filtered, paginated reads are natural tool arguments; their resource URIs exist for context attachment.

The parameterless collections (`hearth://adapters`, `hearth://entities`, `hearth://devices`, `hearth://commands`, `hearth://automations`) are additionally served as concrete resources: templates alone leave `resources/list` empty, and clients that materialize resources as tools only see concrete resources. The bare URI reads the default first page through the same reader as its template; paged reads keep flowing through the template. Parameterized families stay template-only.

v1 resources are read-only and poll-only. Clients poll; nothing bridges resource updates from NATS Device Facts (§2). The `hearth://` namespace is reserved for a future subscription design.

## 7. Project layout

```text
internal/
├── mcpapi/          # NEW: thin wrapper over the official MCP SDK, no Echo dep
│   ├── server.go    # NEW: Config, Server, New, Raw, HTTPHandler (stateless Streamable HTTP)
│   ├── tool.go      # NEW: Handler[I,O], Tool[I,O], Register, HandlerWithRequest
│   ├── doc.go       # NEW: package contract, wrapper owns registration ergonomics only
│   └── *_test.go    # NEW: wrapper + error-mapping tests via official MCP client
├── mcpecho/         # NEW: one-file Echo adapter
│   └── echo.go      # NEW: Mount(e, path, server, middleware...)
└── app/hearthd/
    ├── http_handler.go          # MODIFY: construct mcpapi.Server, register §4 tools, Mount at /mcp
    ├── run.go                   # MODIFY only if lifecycle demands (none expected; same http.Server)
    └── mcp_integration_test.go  # NEW: /mcp tools/list, typed call, invalid input, middleware/context reachability
internal/modules/devices/api/
└── mcp.go           # NEW: devices tool definitions (names, descriptions, thin handlers)
internal/modules/automations/api/
└── mcp.go           # NEW: automations tool definitions
go.mod / go.sum      # MODIFY: add github.com/modelcontextprotocol/go-sdk
docs/adr/0023-serve-mcp-from-core-http-server.md  # NEW: transport decision
```

`http_handler.go` change shape (illustrative, not a patch):

```go
mcpServer := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
devicesapi.RegisterMCP(mcpServer, devices)         // 15 tools, thin handlers in devices/api/mcp.go
automationsapi.RegisterMCP(mcpServer, automations) // 8 tools, thin handlers in automations/api/mcp.go
mcpecho.Mount(router, "/mcp", mcpServer)
```

No config struct changes: `KnownFields(true)` YAML stays untouched and MCP is always on. Persistence, NATS, and schema non-goals are in §2.

## 8. Deliverables (ordered)

1. **[D1] (M): `mcpapi` wrapper + SDK dependency.** `New`, `Register`, `Raw`, stateless `HTTPHandler`, `HandlerWithRequest` escape hatch. Depends on: none. Owning paths: `internal/mcpapi/`, `go.mod`. Acceptance: **[A1]** unit tests using the official MCP client (not hand-built JSON-RPC) prove typed round-trip and invalid-input rejection (§5 row 2).
2. **[D2] (M): Echo mount + assembly.** `mcpecho.Mount`, wiring in `NewHTTPHandler` at `/mcp` (always on). Depends on: D1. Owning paths: `internal/mcpecho/`, `internal/app/hearthd/http_handler.go`. Acceptance: **[A2]** `tools/list` over `/mcp` succeeds; **[A3]** Echo-set `context.Context` values reach tool handlers; **[A4]** no new config keys.
3. **[D3] (L): Devices tools (15).** Table §4.1. Depends on: D2. Owning paths: `internal/modules/devices/api/mcp.go`. Acceptance: **[A5]** each tool exercises its service method through a live `/mcp` call; **[A6]** `execute_entity_command` blocks to terminal outcome and a dropped call still leaves a readable record via `get_command`.
4. **[D4] (L): Automations tools (8).** Table §4.2. Depends on: D2. Owning paths: `internal/modules/automations/api/mcp.go`. Acceptance: **[A7]** a create, run, and history round-trip over `/mcp` only.
5. **[D5] (L): Resources.** Catalog §6 via `Raw()` and SDK resource APIs; poll-only. Depends on: D3, D4. Acceptance: **[A8]** every §6 URI reads through `/mcp` and matches its Huma GET response field-for-field (modulo envelope).
6. **[D6] (M): Error mapping.** §5 table, all three rows, including 409 disabled-command and per-status Command failures. Depends on: D3, D4. Acceptance: **[A9]** each failure class asserted as `isError: true` plus structured `failure_code`; **[A10]** malformed input never reaches handlers.
7. **[D7] (S): Decision record.** ADR-0023 (transport) plus a 1-2 line architecture constraint if accepted. Depends on: D2. Acceptance: **[A11]** ADR merged.
8. **[D8] (M): Validation.** `mise run validate` plus a smoke of `/mcp` against a simulator-backed stack. Depends on: D1-D6. Acceptance: **[A12]** validate green; **[A13]** simulator smoke covers list, get, and command execution over MCP.

## 9. Risks

- **Dual-maintenance drift (reads as tool + resource).** Mitigation: both call the same service method with shared I/O types, and A8's conformance check asserts equivalence.
- **Blocking command calls hold HTTP connections up to the operation deadline** (10s for power `set`). Mitigation: §4.3's commit-before-dispatch makes drops safe; document the pattern and add a timeout test.
- **Official Go SDK API churn** (pre-1.0 surface). Mitigation: SDK imports are confined to `internal/mcpapi` (§3.3), so churn touches one package.
- **Scope weight (23 tools + 15 resource templates).** Mitigation: D3/D4 are mechanical replicas of Huma operations; ordering lets devices tools land and prove the pattern before automations.

## 10. Open questions

- [ ] Whether `GET /healthz` and `/readyz` need MCP equivalents (health-as-tool). Owner: reviewer; current answer is no (process health stays HTTP).

---
Phase: DONE, spec written, awaiting review.
