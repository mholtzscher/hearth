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
	factConnection      *natsgo.Conn
	jetstream           jetstream.JetStream
	observationConsumer *devicesnats.ObservationConsumer
	entityEventConsumer *devicesnats.EntityEventConsumer
	dispatcher          *devicesnats.DeviceFactDispatcher
}

// NewRuntimeReadiness assembles the readiness dependencies. Both NATS
// connections are required: the shared ingest/request connection and the
// dedicated Device Fact publication connection, since a Core process that can
// record device work but cannot announce it is not ready.
func NewRuntimeReadiness(
	database *sql.DB,
	connection *natsgo.Conn,
	factConnection *natsgo.Conn,
	js jetstream.JetStream,
	observationConsumer *devicesnats.ObservationConsumer,
	entityEventConsumer *devicesnats.EntityEventConsumer,
	dispatcher *devicesnats.DeviceFactDispatcher,
) *RuntimeReadiness {
	return &RuntimeReadiness{
		database: database, connection: connection, factConnection: factConnection, jetstream: js,
		observationConsumer: observationConsumer, entityEventConsumer: entityEventConsumer,
		dispatcher: dispatcher,
	}
}

func (readiness *RuntimeReadiness) Check(ctx context.Context) error {
	if readiness == nil || readiness.database == nil || readiness.connection == nil ||
		readiness.factConnection == nil || readiness.jetstream == nil || readiness.dispatcher == nil {
		return errors.New("runtime dependencies are not initialized")
	}
	if err := readiness.database.PingContext(ctx); err != nil {
		return errors.New("SQLite is unavailable")
	}
	if !readiness.connection.IsConnected() {
		return errors.New("NATS is disconnected")
	}
	if !readiness.factConnection.IsConnected() {
		return errors.New("device fact NATS is disconnected")
	}
	// Readiness requires an active dispatcher but no subscriber, no fact stream
	// and no proof that any publication was received.
	if !readiness.dispatcher.Active() {
		return errors.New("device fact dispatcher is inactive")
	}
	if err := devicesnats.ValidateObservationResources(ctx, readiness.jetstream); err != nil {
		return err
	}
	// Entity Event resources are validated for configuration compatibility and
	// current consumption, never for backlog: an empty page of unread history
	// is not a readiness failure and readiness never gates ingestion.
	if err := devicesnats.ValidateEntityEventResources(ctx, readiness.jetstream); err != nil {
		return err
	}
	if !readiness.observationConsumer.Active() {
		return errors.New("observation consumer is inactive")
	}
	if !readiness.entityEventConsumer.Active() {
		return errors.New("entity event consumer is inactive")
	}
	return nil
}

// CommandAdmissionChecker is the narrow readiness seam for Command
// admission. It stays separate from the broad Devices API so HTTP handlers
// can never bypass admission.
type CommandAdmissionChecker interface {
	CommandAdmissionOpen() bool
}

func NewHTTPHandler(
	devices devicesapi.Devices,
	readiness ReadinessChecker,
	commandAdmission CommandAdmissionChecker,
) (http.Handler, huma.API) {
	const statusField = "status"
	router := echo.New()
	router.GET("/healthz", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ok"})
	})
	router.GET("/readyz", func(ctx *echo.Context) error {
		if readiness == nil || readiness.Check(ctx.Request().Context()) != nil || commandAdmission == nil ||
			!commandAdmission.CommandAdmissionOpen() {
			return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusField: "not_ready"})
		}
		return ctx.JSON(http.StatusOK, map[string]string{statusField: "ready"})
	})

	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	v1 := huma.NewGroup(api, "/v1")
	devicesapi.Register(v1, devices)
	return router, api
}
