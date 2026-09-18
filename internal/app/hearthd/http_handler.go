package hearthd

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/mcpecho"
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

// NewHTTPHandler builds the Core HTTP handler.
func NewHTTPHandler(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission AutomationAdmissionChecker,
) (http.Handler, huma.API) {
	handler, api, _ := NewHTTPHandlerWithMCP(
		devices, automations, readiness, commandAdmission, automationAdmission,
	)
	return handler, api
}

// NewHTTPHandlerWithMCP builds the Core HTTP handler and returns its MCP server
// so callers can register extra tools before serving.
//
// The MCP endpoint is always mounted at mcpPath on the same listener with the
// full Devices and Automations catalog registered; it adds no configuration
// keys. Callers may register further tools through the returned
// [mcpapi.Server], which the mounted handler serves even when registration
// happens after this function returns.
//
// The MCP server diagnostics go to the global default logger; use
// [newHTTPHandlerWithMCP] to inject the application logger.
func NewHTTPHandlerWithMCP(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission AutomationAdmissionChecker,
) (http.Handler, huma.API, *mcpapi.Server) {
	return newHTTPHandlerWithMCP(
		devices, automations, readiness, commandAdmission, automationAdmission, nil,
	)
}

// newHTTPHandlerWithMCP builds the Core HTTP handler and returns its MCP server,
// routing MCP diagnostics to logger. A nil logger selects the global default,
// which keeps the exported constructors' behavior unchanged.
func newHTTPHandlerWithMCP(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission AutomationAdmissionChecker,
	logger *slog.Logger,
) (http.Handler, huma.API, *mcpapi.Server) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	// Readiness requires the broker and persistence resources, both consumer
	// lifecycles, and both admission gates. Automation admission is checked
	// beside Command admission because a manual or fact-driven Run needs a
	// device Command to take effect.
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil || commandAdmission == nil ||
			!commandAdmission.CommandAdmissionOpen() || automationAdmission == nil ||
			!automationAdmission.AdmissionOpen() {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	automationsapi.Register(v1, automations)

	mcpServer := mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion, Logger: logger})
	// Every Huma operation is reachable over MCP with equal semantics: each
	// module owns the thin tool and resource adapters over its own service.
	devicesapi.RegisterMCP(mcpServer, devices)
	automationsapi.RegisterMCP(mcpServer, automations)
	mcpecho.Mount(router, mcpPath, mcpServer)
	return router, api, mcpServer
}
