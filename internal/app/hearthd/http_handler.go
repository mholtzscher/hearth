package hearthd

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/mcpecho"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/api"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/api"
)

const (
	// mcpPath is the MCP endpoint on the existing HTTP listener.
	mcpPath = "/mcp"
	// mcpName and mcpVersion mirror the Huma server identity below.
	mcpName    = "hearth"
	mcpVersion = "1.0.0"
)

// CommandAdmissionChecker is the narrow readiness seam for Command
// admission. It stays separate from the broad Devices API so HTTP handlers
// can never bypass admission.
type CommandAdmissionChecker interface {
	CommandAdmissionOpen() bool
}

// OperationAdmissionChecker keeps readiness separate from operation APIs.
// Automation and Function services each supply their own independent gate.
type OperationAdmissionChecker interface {
	AdmissionOpen() bool
}

// newMCPServer builds the Core MCP catalog. Application assembly builds it
// before the agent, so the agent bridges exactly the catalog the /mcp endpoint
// serves and a catalog problem fails startup instead of serving a tool-less
// agent. A nil logger routes MCP diagnostics to [slog.Default].
func newMCPServer(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	notifications notificationsapi.Notifications,
	adapters adaptersapi.Adapters,
	functions adaptersapi.Functions,
	logger *slog.Logger,
) *mcpapi.Server {
	mcpServer := mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion, Logger: logger})
	// Every Huma operation is reachable over MCP with equal semantics: each
	// module owns the thin tool and resource adapters over its own service.
	devicesapi.RegisterMCP(mcpServer, devices)
	automationsapi.RegisterMCP(mcpServer, automations)
	notificationsapi.RegisterMCP(mcpServer, notifications)
	adaptersapi.RegisterMCP(mcpServer, adapters)
	adaptersapi.RegisterFunctionsMCP(mcpServer, functions)
	return mcpServer
}

// newHTTPHandler assembles the Core HTTP, Huma, SSE, and MCP surfaces from the
// services and the already-built MCP catalog. It is the single place any
// transport registers routes, so the HTTP, OpenAPI, and MCP surfaces cannot
// drift apart. A nil agent registers no agent routes and keeps readiness
// closed; production always builds the required agent.
func newHTTPHandler(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	notifications notificationsapi.Notifications,
	adapters adaptersapi.Adapters,
	functions adaptersapi.Functions,
	agentOperations agent.Operations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission OperationAdmissionChecker,
	functionAdmission OperationAdmissionChecker,
	mcpServer *mcpapi.Server,
) (http.Handler, huma.API) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	// Readiness requires the broker and persistence resources, both consumer
	// lifecycles, and Command, Automation, Function, and Agent admission gates. A notification
	// write failure does not add a remembered readiness fault. Automation admission
	// is checked beside Command admission because a manual or fact-driven Run needs a
	// device Command to take effect, and Agent admission is checked because an
	// agent turn drives those same Device Commands through the MCP catalog.
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil || commandAdmission == nil ||
			!commandAdmission.CommandAdmissionOpen() || automationAdmission == nil ||
			!automationAdmission.AdmissionOpen() || agentOperations == nil ||
			!agentOperations.AdmissionOpen() || functionAdmission == nil || !functionAdmission.AdmissionOpen() {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	automationsapi.Register(v1, automations)
	notificationsapi.Register(v1, notifications)
	adaptersapi.Register(v1, adapters)
	adaptersapi.RegisterFunctions(v1, functions)
	agent.Register(v1, agentOperations)
	// The turn stream needs the Echo router directly: Huma has no SSE primitive,
	// so it stays out of openapi.json while sharing this handler's listener.
	agent.RegisterStream(router, agentOperations)
	mcpecho.Mount(router, mcpPath, mcpServer)
	return router, api
}
