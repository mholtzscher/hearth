package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

// registeredDrain is one started request/reply transport paired with the
// process.cleanup_failed stage its drain reports under.
type registeredDrain struct {
	stage string
	drain interface{ Drain() error }
}

// coreShutdown owns every started resource. Run calls it once on cancellation
// or error; nil fields represent resources that never started. Its explicit
// order keeps dependencies alive until admitted work finishes, and keeps HTTP
// serving draining readiness until then. Cleanup continues after errors.
type coreShutdown struct {
	runContext          context.Context
	logger              *slog.Logger
	database            *sql.DB
	connection          *natsgo.Conn
	relay               *devicesnats.DeviceFactRelay
	consumers           *coreConsumers
	automationConsumers *automationConsumers
	automationService   *automations.Service
	deviceService       *devices.Service
	cancelDependencies  context.CancelFunc
	maintenance         sync.WaitGroup
	healthSupervisor    *healthSupervisor
	server              *http.Server
	transports          []registeredDrain
}

// run closes admission, joins work, then withdraws dependencies, returning the
// first cleanup error. Run's single defer is its only production caller.
func (shutdown *coreShutdown) run() error {
	var firstErr error
	fail := func(stage string, err error) {
		logCleanupFailure(shutdown.runContext, shutdown.logger, stage, err)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if shutdown.automationConsumers != nil {
		shutdown.automationConsumers.close()
	}
	if shutdown.automationService != nil {
		shutdown.automationService.StopAdmission()
	}
	if shutdown.deviceService != nil {
		shutdown.deviceService.StopCommandAdmission()
	}
	// Ignore process cancellation: admitted work runs to its own deadline.
	// Join Automation Runs first because their Steps depend on Commands.
	if shutdown.automationService != nil {
		_ = shutdown.automationService.WaitRuns(context.Background())
	}
	if shutdown.deviceService != nil {
		_ = shutdown.deviceService.WaitCommands(context.Background())
	}
	if shutdown.cancelDependencies != nil {
		shutdown.cancelDependencies()
	}
	shutdown.maintenance.Wait()
	if shutdown.healthSupervisor != nil {
		shutdown.healthSupervisor.Stop()
	}
	if shutdown.server != nil {
		fail("shutdown_http", shutdown.stopHTTP())
	}
	if shutdown.consumers != nil {
		shutdown.consumers.close()
	}
	if shutdown.relay != nil {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
		drainErr := shutdown.relay.Drain(drainContext)
		cancelDrain()
		fail("drain_device_facts", drainErr)
	}
	for _, transport := range slices.Backward(shutdown.transports) {
		fail(transport.stage, transport.drain.Drain())
	}
	if shutdown.connection != nil {
		drainErr := shutdown.connection.Drain()
		if errors.Is(drainErr, natsgo.ErrConnectionClosed) {
			drainErr = nil
		}
		if drainErr != nil {
			drainErr = fmt.Errorf("drain NATS connection: %w", drainErr)
		}
		fail("drain_nats", drainErr)
		shutdown.connection.Close()
	}
	if shutdown.database != nil {
		fail("close_database", shutdown.database.Close())
	}
	return firstErr
}

// stopHTTP force-closes connections when graceful shutdown times out. An
// unfinished request can outlast the window while net/http waits for headers;
// timing out is expected and does not turn cancellation into a process failure.
func (shutdown *coreShutdown) stopHTTP() error {
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := shutdown.server.Shutdown(shutdownContext)
	cancelShutdown()
	if shutdownErr == nil {
		return nil
	}
	if errors.Is(shutdownErr, context.DeadlineExceeded) {
		_ = shutdown.server.Close()
		return nil
	}
	return failStage("shutdown_http", fmt.Errorf("shutdown HTTP: %w", shutdownErr))
}

// coreConsumers owns Observation and Entity Event consumers. Their callback
// context survives process and dependency cancellation so in-flight records can
// commit during drain. It is canceled only after both consumers drain or stop.
type coreConsumers struct {
	callbackContext context.Context
	cancelCallbacks context.CancelFunc
	observations    *devicesnats.ObservationConsumer
	entityEvents    *devicesnats.EntityEventConsumer
}

// newCoreConsumers gives both consumers a context detached from parent cancellation.
func newCoreConsumers(parent context.Context) *coreConsumers {
	callbackContext, cancelCallbacks := context.WithCancel(context.WithoutCancel(parent))
	return &coreConsumers{callbackContext: callbackContext, cancelCallbacks: cancelCallbacks}
}

// startObservations subscribes the Observation consumer under the consumer
// lifecycle context, so its projector keeps a live context through shutdown.
func (consumers *coreConsumers) startObservations(
	durable jetstream.Consumer,
	validator *contractsv1.Validator,
	projector devicesnats.ObservationProjector,
	logger *slog.Logger,
) error {
	observations, err := devicesnats.StartObservationConsumer(
		consumers.callbackContext, durable, validator, projector, logger,
	)
	if err != nil {
		return err
	}
	consumers.observations = observations
	return nil
}

// startEntityEvents subscribes the Entity Event consumer under the same
// consumer lifecycle context as the Observation consumer.
func (consumers *coreConsumers) startEntityEvents(
	durable jetstream.Consumer,
	validator *contractsv1.Validator,
	recorder devicesnats.EntityEventRecorder,
	logger *slog.Logger,
) error {
	entityEvents, err := devicesnats.StartEntityEventConsumer(
		consumers.callbackContext, durable, validator, recorder, logger,
	)
	if err != nil {
		return err
	}
	consumers.entityEvents = entityEvents
	return nil
}

// drain stops Entity Events before Observations, skipping consumers that never started.
func (consumers *coreConsumers) drain() {
	if consumers.entityEvents != nil {
		drainConsumer(consumers.entityEvents)
	}
	if consumers.observations != nil {
		drainConsumer(consumers.observations)
	}
}

// close drains before canceling callbacks so in-flight records can commit.
func (consumers *coreConsumers) close() {
	consumers.drain()
	consumers.cancelCallbacks()
}

// consumerDrain is the lifecycle shared by Entity Event and Observation consumers.
type consumerDrain interface {
	Drain()
	Stop()
	Closed() <-chan struct{}
}

// drainConsumer allows in-flight callbacks a bounded drain window, then stops
// delivery. Unprocessed or unacknowledged input stays in the stream for restart.
func drainConsumer(consumer consumerDrain) {
	consumer.Drain()
	drainContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	select {
	case <-consumer.Closed():
	case <-drainContext.Done():
		consumer.Stop()
	}
}
