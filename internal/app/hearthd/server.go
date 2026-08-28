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

func NewHTTPHandler(devices devicesapi.Devices, readiness ReadinessChecker) (http.Handler, huma.API) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	return router, api
}
