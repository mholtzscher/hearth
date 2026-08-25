package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type ReadinessChecker interface {
	Check(context.Context) error
}

type RuntimeReadiness struct {
	database            *sql.DB
	connection          *natsgo.Conn
	jetstream           jetstream.JetStream
	observationConsumer *devicesnats.ObservationConsumer
}

func NewRuntimeReadiness(
	database *sql.DB,
	connection *natsgo.Conn,
	js jetstream.JetStream,
	consumer *devicesnats.ObservationConsumer,
) *RuntimeReadiness {
	return &RuntimeReadiness{
		database: database, connection: connection, jetstream: js, observationConsumer: consumer,
	}
}

func (readiness *RuntimeReadiness) Check(ctx context.Context) error {
	if readiness == nil || readiness.database == nil || readiness.connection == nil || readiness.jetstream == nil {
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
	return nil
}

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

// NewHTTPHandler configures process-global Huma error behavior and must only
// be called during single-threaded application or test setup.
func NewHTTPHandler(devices devicesapi.Devices, readiness ReadinessChecker) (http.Handler, huma.API) {
	huma.NewError = newHumaError
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	entities := huma.NewGroup(api, "/v1/entities")
	devicesapi.Register(entities, devices)
	return router, api
}
