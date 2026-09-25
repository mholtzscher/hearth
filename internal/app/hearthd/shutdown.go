package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// registeredDrain is one started request/reply transport paired with the
// process.cleanup_failed stage its drain reports under.
type registeredDrain struct {
	stage string
	drain interface{ Drain() error }
}

// coreShutdown owns every started resource. Run calls it once on cancellation
// or error; nil fields represent resources that never started. Its explicit
// order keeps dependencies alive until admitted work finishes, joins the
// history pruning worker before SQLite closes, and keeps HTTP serving draining
// readiness until then. Cleanup continues after errors.
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
	agentService        *agent.Service
	cancelDependencies  context.CancelFunc
	historyPruneWorker  *lifecycle.WorkerHandle
	heldStateWorker     *lifecycle.WorkerHandle
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

	if shutdown.heldStateWorker != nil {
		fail("stop_held_state_scheduler", shutdown.heldStateWorker.Stop(context.Background()))
	}
	if shutdown.automationConsumers != nil {
		shutdown.automationConsumers.close()
	}
	// Close admission before joining work: a drained worker must not be able to
	// admit new work, and a still-draining dependency must keep serving the
	// workers that already hold a reservation.
	shutdown.closeAdmissionAndJoinWorkers()
	// Canceling the dependency context closes the agent's MCP client and server
	// sessions: no joined turn can call a tool after this point.
	if shutdown.cancelDependencies != nil {
		shutdown.cancelDependencies()
	}
	fail("stop_history_pruning", shutdown.stopHistoryPruning())
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

// closeAdmissionAndJoinWorkers closes every admission gate, then joins admitted
// work in dependency order. The agent drains first because one of its turns
// reaches Devices and Automations through the MCP catalog, so no turn may still
// be running when those modules begin to drain. Process cancellation is
// deliberately ignored: admitted work runs to its own deadline.
func (shutdown *coreShutdown) closeAdmissionAndJoinWorkers() {
	if shutdown.agentService != nil {
		shutdown.agentService.StopAdmission()
	}
	if shutdown.automationService != nil {
		shutdown.automationService.StopAdmission()
	}
	if shutdown.deviceService != nil {
		shutdown.deviceService.StopAdmission()
	}
	if shutdown.agentService != nil {
		_ = shutdown.agentService.Drain(context.Background())
	}
	// Join Automation Runs before Commands because their Steps depend on Commands.
	if shutdown.automationService != nil {
		_ = shutdown.automationService.Drain(context.Background())
	}
	if shutdown.deviceService != nil {
		_ = shutdown.deviceService.Drain(context.Background())
	}
}

// stopHistoryPruning joins the retention worker before SQLite closes. Its
// context is already canceled by run, and Stop waits unbounded so no pruning
// transaction can be in flight once the database closes. A never-started worker
// is a no-op.
func (shutdown *coreShutdown) stopHistoryPruning() error {
	if shutdown.historyPruneWorker == nil {
		return nil
	}
	return shutdown.historyPruneWorker.Stop(context.Background())
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
	observations    *platformnats.Consumer
	entityEvents    *platformnats.Consumer
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

// drainConsumer bounds the wait, not the callbacks. Unacknowledged input stays
// in the stream for restart; Stop cannot interrupt an already-started drain.
func drainConsumer(consumer *platformnats.Consumer) {
	drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelDrain()
	_ = consumer.Drain(drainContext)
}
