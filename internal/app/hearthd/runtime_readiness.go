package hearthd

import (
	"context"
	"database/sql"
	"errors"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// ReadinessChecker reports whether the Core runtime can currently serve work.
type ReadinessChecker interface {
	Check(context.Context) error
}

// RuntimeReadiness checks Core infrastructure, resources, and consumer activity.
type RuntimeReadiness struct {
	database            *sql.DB
	connection          *natsgo.Conn
	jetstream           jetstream.JetStream
	observationConsumer *platformnats.Consumer
	entityEventConsumer *platformnats.Consumer
	relay               *devicesnats.DeviceFactRelay
	automationConsumer  automationActivity
	heldStateWorker     *lifecycle.WorkerHandle
}

// NewRuntimeReadiness assembles the readiness dependencies. One shared NATS
// connection carries every Core subscription and every Device Fact publication,
// so a Core process either has the broker or does not; readiness never requires
// a subscriber, a consumer Core does not own, or an empty backlog.
func NewRuntimeReadiness(
	database *sql.DB,
	connection *natsgo.Conn,
	js jetstream.JetStream,
	observationConsumer *platformnats.Consumer,
	entityEventConsumer *platformnats.Consumer,
	relay *devicesnats.DeviceFactRelay,
	automationConsumer automationActivity,
	heldStateWorkers ...*lifecycle.WorkerHandle,
) *RuntimeReadiness {
	readiness := &RuntimeReadiness{
		database: database, connection: connection, jetstream: js,
		observationConsumer: observationConsumer, entityEventConsumer: entityEventConsumer,
		relay: relay, automationConsumer: automationConsumer,
	}
	if len(heldStateWorkers) > 0 {
		readiness.heldStateWorker = heldStateWorkers[0]
	}
	return readiness
}

// Check verifies every dependency required for Core readiness in dependency order.
func (readiness *RuntimeReadiness) Check(ctx context.Context) error {
	if readiness == nil || readiness.database == nil || readiness.connection == nil ||
		readiness.jetstream == nil || readiness.relay == nil || readiness.automationConsumer == nil {
		return errors.New("runtime dependencies are not initialized")
	}
	if readiness.heldStateWorker != nil {
		select {
		case <-readiness.heldStateWorker.Closed():
			return errors.New("held-state scheduler is inactive")
		default:
		}
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
	// The automations-owned Device Fact consumer is validated against its exact
	// broker configuration and live consumption. Its backlog is never a
	// readiness failure: automatic admission recovers what the stream retained.
	if err := automationsnats.ValidateDeviceFactConsumer(
		ctx, readiness.jetstream, devicesnats.DeviceFactStreamName,
	); err != nil {
		return err
	}
	if !readiness.automationConsumer.Active() {
		return errors.New("automation device fact consumer is inactive")
	}
	return nil
}
