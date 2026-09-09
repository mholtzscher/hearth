package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

type ReadinessChecker interface {
	Check(context.Context) error
}

type RuntimeReadiness struct {
	database            *sql.DB
	connection          *natsgo.Conn
	jetstream           jetstream.JetStream
	observationConsumer *devicesnats.ObservationConsumer
	scheduler           AutomationSchedulerHealth
}

// AutomationSchedulerHealth is the narrow readiness seam for scheduled-admission
// persistence. The automations Service satisfies it. Scheduler health is only
// one readiness input: device dependencies, execution admission, and the sticky
// executor fault stay separate gates.
type AutomationSchedulerHealth interface {
	AutomationSchedulerHealthy() bool
}

// AutomationReadiness is the HTTP readiness seam: automation management and
// history plus the scheduler gate. The automations Service satisfies it; test
// stubs implement it narrowly. It lives here so the api package stays untouched.
type AutomationReadiness interface {
	automationsapi.Automations
	AutomationSchedulerReady() bool
}

func NewRuntimeReadiness(
	database *sql.DB,
	connection *natsgo.Conn,
	js jetstream.JetStream,
	consumer *devicesnats.ObservationConsumer,
	scheduler AutomationSchedulerHealth,
) *RuntimeReadiness {
	return &RuntimeReadiness{
		database: database, connection: connection, jetstream: js, observationConsumer: consumer,
		scheduler: scheduler,
	}
}

func (readiness *RuntimeReadiness) Check(ctx context.Context) error {
	if readiness == nil || readiness.database == nil || readiness.connection == nil || readiness.jetstream == nil ||
		readiness.scheduler == nil {
		return errors.New("runtime dependencies are not initialized")
	}
	if err := readiness.database.PingContext(ctx); err != nil {
		return errors.New("SQLite is unavailable")
	}
	if !readiness.connection.IsConnected() {
		return errors.New("NATS is disconnected")
	}
	if err := devicesnats.ValidateObservationResources(ctx, readiness.jetstream); err != nil {
		return err
	}
	if !readiness.observationConsumer.Active() {
		return errors.New("observation consumer is inactive")
	}
	if !readiness.scheduler.AutomationSchedulerHealthy() {
		return errors.New("automation scheduler is unhealthy")
	}
	return nil
}

// CommandAdmissionChecker is the narrow readiness seam for direct Command
// admission. It stays separate from the broad Devices API so HTTP handlers,
// which use ExecuteCommand, can never supply reserved automation Step permission.
type CommandAdmissionChecker interface {
	CommandAdmissionOpen() bool
}

func NewHTTPHandler(
	devices devicesapi.Devices,
	automationService AutomationReadiness,
	definitions *automations.AutomationDefinitionCodec,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
) (http.Handler, huma.API) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil || automationService == nil ||
			!automationService.AutomationExecutionReady() || !automationService.AutomationSchedulerReady() ||
			commandAdmission == nil || !commandAdmission.CommandAdmissionOpen() {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	automationsapi.Register(v1, automationService, definitions)
	return router, api
}
