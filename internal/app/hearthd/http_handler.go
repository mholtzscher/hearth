package hearthd

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/mcpecho"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
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

// AutomationAdmissionChecker is the narrow readiness seam for Automation
// admission. It stays separate from the broad Automations API so HTTP handlers
// can never bypass admission.
type AutomationAdmissionChecker interface {
	AdmissionOpen() bool
}

// newMCPServer builds the Core MCP catalog. Application assembly builds it
// before the agent, so the agent bridges exactly the catalog the /mcp endpoint
// serves and a catalog problem fails startup instead of serving a tool-less
// agent. A nil logger routes MCP diagnostics to [slog.Default].
func newMCPServer(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	logger *slog.Logger,
) *mcpapi.Server {
	mcpServer := mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion, Logger: logger})
	// Every Huma operation is reachable over MCP with equal semantics: each
	// module owns the thin tool and resource adapters over its own service.
	devicesapi.RegisterMCP(mcpServer, devices)
	automationsapi.RegisterMCP(mcpServer, automations)
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
	agentOperations agent.Operations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission AutomationAdmissionChecker,
	mcpServer *mcpapi.Server,
) (http.Handler, huma.API) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	// Readiness requires the broker and persistence resources, both consumer
	// lifecycles, and every admission gate. Automation admission is checked
	// beside Command admission because a manual or fact-driven Run needs a
	// device Command to take effect, and Agent admission is checked because an
	// agent turn drives those same Device Commands through the MCP catalog.
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil || commandAdmission == nil ||
			!commandAdmission.CommandAdmissionOpen() || automationAdmission == nil ||
			!automationAdmission.AdmissionOpen() || agentOperations == nil ||
			!agentOperations.AdmissionOpen() {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	automationsapi.Register(v1, automations)
	agent.Register(v1, agentOperations)
	// The turn stream needs the Echo router directly: Huma has no SSE primitive,
	// so it stays out of openapi.json while sharing this handler's listener.
	agent.RegisterStream(router, agentOperations)
	mcpecho.Mount(router, mcpPath, mcpServer)
	return router, api
}
