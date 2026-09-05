package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		return &readinessCheckError{
			reasonCode: readinessCheckFailedReason, err: errors.New("runtime dependencies are not initialized"),
		}
	}
	if err := readiness.database.PingContext(ctx); err != nil {
		return &readinessCheckError{reasonCode: "sqlite_unavailable", err: fmt.Errorf("SQLite is unavailable: %w", err)}
	}
	if !readiness.connection.IsConnected() {
		return &readinessCheckError{
			reasonCode: "nats_disconnected",
			err:        errors.New("NATS is disconnected"),
		}
	}
	if err := devicesnats.ValidateObservationResources(ctx, readiness.jetstream); err != nil {
		return &readinessCheckError{reasonCode: "jetstream_unavailable", err: err}
	}
	if !readiness.observationConsumer.Active() {
		return &readinessCheckError{
			reasonCode: "observation_consumer_inactive",
			err:        errors.New("observation consumer is inactive"),
		}
	}
	return nil
}

// readinessCheckError carries a fixed safe reason code for readiness logs
// while preserving the underlying error text and chain for existing consumers.
// It is not a public health model and adds no response field.
type readinessCheckError struct {
	reasonCode string
	err        error
}

func (err *readinessCheckError) Error() string { return err.err.Error() }

func (err *readinessCheckError) Unwrap() error { return err.err }

// readinessCheckFailedReason marks checker errors without a specific cause.
const readinessCheckFailedReason = "readiness_check_failed"

// readinessFailureReason reports the fixed reason code carried by err, mapping
// unknown checker errors to readiness_check_failed without matching error strings.
func readinessFailureReason(err error) string {
	if checkErr, ok := errors.AsType[*readinessCheckError](err); ok {
		return checkErr.reasonCode
	}
	return readinessCheckFailedReason
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
