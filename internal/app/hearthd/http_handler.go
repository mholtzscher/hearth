package hearthd

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
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

func NewHTTPHandler(
	devices devicesapi.Devices,
	automations automationsapi.Automations,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
	automationAdmission AutomationAdmissionChecker,
) (http.Handler, huma.API) {
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
	return router, api
}
