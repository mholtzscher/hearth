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
	entityEventConsumer *devicesnats.EntityEventConsumer
	relay               *devicesnats.DeviceFactRelay
}

// NewRuntimeReadiness assembles the readiness dependencies. One shared NATS
// connection carries every Core subscription and every Device Fact publication,
// so a Core process either has the broker or does not; readiness never requires
// a subscriber, a consumer Core does not own, or an empty backlog.
func NewRuntimeReadiness(
	database *sql.DB,
	connection *natsgo.Conn,
	js jetstream.JetStream,
	observationConsumer *devicesnats.ObservationConsumer,
	entityEventConsumer *devicesnats.EntityEventConsumer,
	relay *devicesnats.DeviceFactRelay,
) *RuntimeReadiness {
	return &RuntimeReadiness{
		database: database, connection: connection, jetstream: js,
		observationConsumer: observationConsumer, entityEventConsumer: entityEventConsumer,
		relay: relay,
	}
}

func (readiness *RuntimeReadiness) Check(ctx context.Context) error {
	if readiness == nil || readiness.database == nil || readiness.connection == nil ||
		readiness.jetstream == nil || readiness.relay == nil {
		return errors.New("runtime dependencies are not initialized")
	}
	if err := readiness.database.PingContext(ctx); err != nil {
		return errors.New("SQLite is unavailable")
	}
	if !readiness.connection.IsConnected() {
		return errors.New("NATS is disconnected")
	}
	// Readiness requires the fact stream configuration and an active relay, but
	// no subscriber, no Core-owned consumer and no proof that any publication
	// was received. The relay is active until it faults on a poison row or
	// begins draining, so readiness fails when it can no longer make progress.
	if err := devicesnats.ValidateDeviceFactStream(ctx, readiness.jetstream); err != nil {
		return err
	}
	if !readiness.relay.Active() {
		return errors.New("device fact relay is inactive")
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
