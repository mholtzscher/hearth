# Devices HTTP Seam and Huma Policy — Implementation Spec

**Status:** Ready for task breakdown
**Type:** Refactoring
**Effort:** M (1–3 hours)
**Approved by:** User design confirmation
**Date:** 2026-08-25
**Baseline:** `main` at `037d778`

## Problem and Decision

`internal/modules/devices/api.Handler` and `Register` depend on `*devices.Service`, although the HTTP transport invokes only `GetEntity` and `ExecuteCommand`. Its tests consequently construct the full service and eight-method repository adapters, and the command HTTP test duplicates orchestration already covered by `internal/modules/devices/command_test.go`.

`devices/api.Register` also installs process-global Huma error policy through `sync.Once`. Operation registration should not configure framework policy owned by application assembly.

Define a two-method `Devices` interface in the consuming `devices/api` package. `Handler`, `Register`, and `hearthd.NewHTTPHandler` accept it; the existing `*devices.Service` satisfies it unchanged. Transport tests use direct function-field adapters.

Move Huma error policy to `internal/app/hearthd/http_handler.go`. Every `NewHTTPHandler` call installs the Hearth factory before constructing the Huma API. The constructor is limited to single-threaded startup or test setup and deliberately overwrites prior `huma.NewError` customization.

Keep the stable error envelope and concrete status error in `devices/api`. Export a minimal factory so application assembly can create that wire shape; command-specific construction, including optional `command_id`, remains private to endpoint mapping.

## Implementation Contracts

### Consumer-owned interface

Owner: `internal/modules/devices/api/get_entity.go`.

```go
type Devices interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityView, error)
	ExecuteCommand(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error)
}

type Handler struct {
	devices Devices
}

func Register(api huma.API, devices Devices)
```

The interface contains exactly the use cases invoked by this route family and uses existing domain inputs, outputs, and errors. It owns no lifecycle or framework methods. `*devices.Service` is the production implementation; compilation through existing assembly is sufficient proof of conformance.

`internal/app/hearthd/http_handler.go` carries the same seam through assembly:

```go
// NewHTTPHandler configures process-global Huma error behavior and must only
// be called during single-threaded application or test setup.
func NewHTTPHandler(devices devicesapi.Devices, readiness ReadinessChecker) (http.Handler, huma.API) {
	huma.NewError = newHumaError
	router := echo.New()
	// Existing health, readiness, Huma construction, and registration follow.
}
```

Existing production and simulator callers continue passing `*devices.Service` directly. `NewHTTPHandler` retains both return values and existing readiness behavior.

### Stable status errors

`ErrorBody`, `APIError`, and unexported `statusError` retain their fields and JSON shape. Add this constructor in `internal/modules/devices/api/types.go`:

```go
import "github.com/danielgtaylor/huma/v2"

func NewStatusError(status int, code, message string) huma.StatusError {
	return &statusError{
		status:    status,
		ErrorBody: ErrorBody{Error: APIError{Code: code, Message: message}},
	}
}
```

`NewStatusError` constructs the envelope but does not classify statuses, accept Huma validation details, or accept `command_id`. Existing endpoint helper `apiError` delegates to it:

```go
func apiError(status int, code, message string) error {
	return NewStatusError(status, code, message)
}
```

`apiCommandError` remains private and continues constructing command-aware errors. `Devices` and `NewStatusError` are the only new exported production symbols.

### Process-wide Huma policy

Owner: `internal/app/hearthd/http_handler.go`.

```go
var defaultHumaNewError = huma.NewError

func newHumaError(status int, message string, details ...error) huma.StatusError {
	if status == 0 {
		return devicesapi.NewStatusError(status, "internal_error", message)
	}
	switch status {
	case http.StatusBadRequest,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity:
		return devicesapi.NewStatusError(
			http.StatusBadRequest,
			"invalid_request",
			"invalid request",
		)
	default:
		return defaultHumaNewError(status, message, details...)
	}
}
```

`defaultHumaNewError` captures Huma's factory at package initialization. Each `NewHTTPHandler` call assigns `newHumaError` before `humaecho.New`; calls must not be concurrent. `devices/api.Register` removes `configureErrorsOnce`, `defaultHumaNewError`, the `sync` import, and all `huma.NewError` mutation.

The process hosts one Hearth Huma policy. This preserves the validation-status set, normalization to HTTP 400, stable code and message, status-zero OpenAPI schema behavior, and Huma's default fallback for other statuses.

### Test adapters and ownership

API tests define a package-local `stubDevices` with one function field per `Devices` method. Each method panics as an unexpected call when its field is unset; otherwise it forwards the exact interface arguments and result. `internal/app/hearthd/http_handler_test.go` defines its own equivalent adapter rather than sharing test-only types across packages.

Tests follow behavior ownership:

- `devices/api`: route registration, canonical ID validation, request translation, response mapping, endpoint error mapping, statuses, bodies, and operation metadata.
- `hearthd`: health/readiness, framework-generated malformed-request normalization, process-wide Huma policy, and the complete runtime OpenAPI document.
- `devices`: command lifecycle, persistence, races, failures, and cancellation; these remain covered only at the business layer.

`TestExecuteCommandReturnsSatisfiedResultAndRegistersOpenAPI` captures and asserts the parsed entity ID, operation name, and JSON parameters through `stubDevices`, then returns a satisfied `CommandResult`. It no longer creates a catalog, repository, sender, service, goroutine, or projected observation.

Move malformed bodies such as `{` and `{"operation":"set"}` to an application test that constructs `NewHTTPHandler`, because their stable response depends on application-owned policy.

A non-parallel API-package test installs a sentinel `huma.NewError`, calls `Register`, verifies that the sentinel remains installed, and restores the original with `t.Cleanup`.

## Compatibility Invariants

- `GET /v1/entities/{entity_id}` and `POST /v1/entities/{entity_id}/commands` retain their methods, paths, operation IDs, summaries, tags, declared errors, validation, and response mappings.
- Success and error responses retain all JSON fields and values, statuses, codes, messages, and optional command `command_id` behavior.
- In a configured Hearth server, malformed bodies, unsupported media types, oversized bodies, and Huma structural validation failures return HTTP 400 with code `invalid_request` and message `invalid request`.
- Huma status zero continues producing the existing `StatusError`/`APIError` OpenAPI schemas, including optional `command_id`.
- `removeAutoValidationResponses` continues removing automatic 422 responses from both operations.
- `devices/api.Register` registers and maps operations without mutating package- or process-global policy.
- No repository interface crosses the HTTP seam; business behavior remains testable without HTTP.

## Scope and Files

This is an atomic internal refactor with no data, configuration, deployment, or mixed-version migration. It does not change `devices.Service`, `devices.Repository`, command orchestration, persistence, NATS behavior, or any public HTTP/OpenAPI contract. Do not introduce a shared HTTP package.

Expected changes:

```text
docs/architecture.md
internal/app/hearthd/http_handler.go
internal/app/hearthd/http_handler_test.go
internal/modules/devices/api/command_test.go
internal/modules/devices/api/get_entity.go
internal/modules/devices/api/get_entity_test.go
internal/modules/devices/api/types.go
specs/http-transport-seam.md
```

No other production file should change. Update the existing architecture sentence to state:

> Echo v5 and Huma v2 provide HTTP transport; application assembly constructs them and owns process-global HTTP framework policy, while the `devices` module owns operation registration and transport mapping. OpenAPI is exposed at runtime and is not committed as a generated artifact.

## Acceptance and Verification

- [ ] `devicesapi.Devices` has exactly the two specified methods; `Handler`, `Register`, and `NewHTTPHandler` depend on it, and existing production and simulator assembly pass `*devices.Service` unchanged.
- [ ] `devices/api.Register` no longer imports `sync`, uses `sync.Once`, or changes `huma.NewError`; `NewHTTPHandler` installs `newHumaError` before `humaecho.New` on every non-concurrent call.
- [ ] `newHumaError`, `NewStatusError`, and private command-error construction satisfy the status, fallback, schema, envelope, and `command_id` contracts above.
- [ ] API and application tests use direct `Devices` adapters rather than repository adapters or `devices.NewService` and cover the ownership-specific behavior above.
- [ ] Runtime OpenAPI retains both operations and current `StatusError`/`APIError` schemas; all HTTP paths, statuses, codes, messages, JSON fields, operation IDs, and readiness behavior remain unchanged.
- [ ] `docs/architecture.md` records application ownership of process-global HTTP framework policy, and only the listed files change.
- [ ] Focused checks pass: `go test ./internal/modules/devices/api ./internal/app/hearthd`.
- [ ] The repository gate passes: `devenv test`, followed by `git diff --check` and `git status --short` inspection.

## Risk Boundaries

Repeated global assignment would race under concurrent constructors; the approved startup/test-setup lifecycle is therefore an implementation contract, and affected tests must not run in parallel. Installing the factory before `humaecho.New` and retaining status-zero and runtime OpenAPI assertions protect schema and error compatibility. If another product HTTP module later needs the same error contract, revisit ownership then rather than expanding this refactor.
