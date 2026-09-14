package hearthd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const (
	retentionPruneInterval = time.Hour
	shutdownTimeout        = 5 * time.Second
	httpReadHeaderTimeout  = 5 * time.Second
	natsReconnectWait      = 250 * time.Millisecond
)

// runStageError identifies the startup stage that failed without echoing
// configuration values or upstream connection details.
type runStageError struct {
	stage string
	err   error
}

func (err *runStageError) Error() string {
	return "hearthd stage " + err.stage + " failed: " + err.err.Error()
}

func (err *runStageError) Unwrap() error { return err.err }

// ErrorStage reports the failed startup stage carried by err, or "run" when
// the error carries no stage. Executables use it for the process.failed stage
// field without logging the underlying error text.
func ErrorStage(err error) string {
	if stageErr, ok := errors.AsType[*runStageError](err); ok {
		return stageErr.stage
	}
	return "run"
}

func failStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &runStageError{stage: stage, err: err}
}

//nolint:funlen,gocognit // Linear lifecycle keeps drain and teardown order explicit.
func Run(
	ctx context.Context,
	config Config,
	logger *slog.Logger,
) error {
	if err := config.Validate(); err != nil {
		return failStage("validate_config", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With(slog.String("component", "process"))
	coreLogger := logger.With(slog.String("component", "core"))
	devicesLogger := logger.With(slog.String("component", "devices"))
	automationsLogger := logger.With(slog.String("component", "automations"))
	natsLogger := logger.With(slog.String("component", "nats"))
	catalog, catalogErr := devices.NewBuiltinTypeCatalog()
	if catalogErr != nil {
		return failStage("load_type_catalog", catalogErr)
	}
	database, openErr := platformdb.Open(ctx, config.SQLitePath)
	if openErr != nil {
		return failStage("open_database", openErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "close_database", database.Close())
	}()
	if err := platformdb.Migrate(ctx, database); err != nil {
		return failStage("migrate_database", err)
	}
	logStartupStage(ctx, coreLogger, "database_migrated")
	repository := devices.NewSQLiteRepository(database, catalog)
	startupTime := time.Now().UTC()
	// Interrupted records are committed before either transport opens; their
	// Command history is authoritative and no Device Fact is published for Core's
	// own startup interruption.
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return failStage("interrupt_commands", fmt.Errorf("interrupt active commands: %w", err))
	}
	logStartupStage(ctx, coreLogger, "active_commands_interrupted")
	// Automation Runs are interrupted directly through the repository seam, in
	// the same pre-NATS startup window as Commands. The automation service is
	// constructed later, once the devices-facing seam exists, and it owns no
	// workers yet, so the repository is the honest seam here. Interrupted records
	// are committed before either transport opens, so no stale Run can be advanced
	// and no fact-driven or manual Run can observe a half-open gate.
	automationRepository := automations.NewSQLiteRepository(database, automations.AutomationDependencies{})
	if err := automationRepository.InterruptActiveRuns(
		ctx, startupTime, automations.AutomationFailureCoreRestarted,
	); err != nil {
		return failStage("interrupt_automations", fmt.Errorf("interrupt active automation runs: %w", err))
	}
	automationsLogger.WarnContext(ctx, "automation runs interrupted",
		slog.String("event", "automation.run_interrupted"),
		slog.String("reason", automations.AutomationFailureCoreRestarted),
	)
	logStartupStage(ctx, coreLogger, "active_automation_runs_interrupted")
	// Observation and Entity Event pruning run only on the hourly pass below, so
	// startup never sweeps retained history and uptime under one hour means no
	// sweep yet.

	connection, connectErr := connectCoreNATS(ctx, config.NATSURL, natsLogger)
	if connectErr != nil {
		return mapStartupCancellation(ctx, connectErr)
	}
	defer connection.Close()
	validator, compileErr := contractsv1.Compile()
	if compileErr != nil {
		return failStage("compile_schemas", fmt.Errorf("compile wire schemas: %w", compileErr))
	}
	js, jetStreamErr := jetstream.New(connection)
	if jetStreamErr != nil {
		return failStage(
			"provision_jetstream",
			fmt.Errorf("create JetStream client: %w", jetStreamErr),
		)
	}
	// The Device Fact stream is provisioned and validated before the relay and
	// before any transport that can commit a fact, so a wrong stream
	// configuration fails startup instead of accepting evidence that nothing can
	// publish durably.
	if streamErr := devicesnats.ProvisionDeviceFactStream(ctx, js); streamErr != nil {
		return mapStartupCancellation(ctx, failStage("provision_jetstream", streamErr))
	}
	// The relay publishes from the durable outbox the repository owns, so it
	// starts before the first fact-producing consumer and before any transport.
	// Its drain runs after every consumer that can commit a fact has drained, and
	// the shared connection outlives it so the drain can still publish what those
	// consumers committed.
	relay, relayErr := devicesnats.StartDeviceFactRelay(js, repository, validator, natsLogger)
	if relayErr != nil {
		return failStage("start_device_facts", relayErr)
	}
	defer func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelDrain()
		logCleanupFailure(ctx, processLogger, "drain_device_facts", relay.Drain(drainContext))
	}()
	commandSender := devicesnats.NewCommandSender(connection, validator)
	service := devices.NewService(
		devices.SQLiteStores(repository),
		commandSender,
		catalog,
		devices.Dependencies{Logger: devicesLogger, DeviceFacts: relay},
	)
	// The devices service implements the read-only devices-facing seam the
	// automations module consumes, so construction order is devices first.
	automationService := automations.NewService(
		automationRepository,
		service,
		automations.AutomationDependencies{Logger: automationsLogger},
	)
	durable, provisionErr := devicesnats.ProvisionObservationResources(ctx, js)
	if provisionErr != nil {
		return mapStartupCancellation(ctx, failStage("provision_jetstream", provisionErr))
	}
	// Both durable resources are provisioned and validated before any
	// transport starts, so a configured stream or consumer mismatch fails
	// startup instead of accepting traffic it cannot record.
	entityEventConsumer, entityEventProvisionErr := devicesnats.ProvisionEntityEventResources(ctx, js)
	if entityEventProvisionErr != nil {
		return mapStartupCancellation(ctx, failStage("provision_jetstream", entityEventProvisionErr))
	}
	// The automation consumer rides the Device Fact stream, which devices owns.
	// Provisioning validates the exact live configuration before any transport
	// starts, so a mismatched durable consumer fails startup instead of
	// admitting facts under unexpected delivery semantics.
	automationDurable, automationProvisionErr := automationsnats.ProvisionDeviceFactConsumer(
		ctx, js, devicesnats.DeviceFactStreamName,
	)
	if automationProvisionErr != nil {
		return mapStartupCancellation(ctx, failStage("provision_jetstream", automationProvisionErr))
	}
	logStartupStage(ctx, coreLogger, "jetstream_provisioned")
	// Current command workers must retain observation and health dependencies
	// beyond shutdown cancellation, including on error exits. Durable consumer
	// callbacks never share dependencyContext: it is canceled before transports
	// drain, which would abort a report or observation that already entered
	// SQLite with context.Canceled. The consumers own a detached lifecycle
	// context that is canceled only after both have drained or stopped, so an
	// already dispatched callback always reaches its commit.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	consumers := newCoreConsumers(ctx)
	defer consumers.close()
	// Automatic admission starts before the inbound Fact producers below, so
	// every Observation or Entity Event that can commit a Fact already has a
	// consumer able to admit it. A first-time consumer begins at the tail; an
	// existing one resumes its durable acknowledgement floor.
	automationFactConsumers := newAutomationConsumers(ctx, automationsLogger)
	defer automationFactConsumers.close()
	var maintenance sync.WaitGroup
	// Every component that can execute work starts below, so the execution
	// cleanup is installed before the first of them. It is registered after the
	// database, NATS connection, consumer, and relay teardowns above, so Go's
	// reverse defer order always runs it before those dependencies are
	// withdrawn. Every teardown registered after it is wrapped with
	// [executionCleanup.teardown], so the health supervisor and the request/reply
	// transports wait for the same cleanup instead of running ahead of it.
	//
	// cleanup.run also cancels dependencyContext, so exactly one path cancels
	// shared observation, health, and persistence dependencies.
	cleanup := &executionCleanup{
		automationService:   automationService,
		automationConsumers: automationFactConsumers,
		deviceService:       service,
		cancelDependencies:  cancelDependencies,
		maintenance:         &maintenance,
	}
	defer cleanup.run()
	sessions, sessionErr := devicesnats.StartSessionServer(connection, validator, service, service, natsLogger)
	if sessionErr != nil {
		return failStage("start_transports", sessionErr)
	}
	defer cleanup.teardown(func() {
		logCleanupFailure(ctx, processLogger, "drain_session_server", sessions.Drain())
	})()
	availability, availabilityErr := devicesnats.StartEntityAvailabilityServer(
		connection, validator, service, natsLogger,
	)
	if availabilityErr != nil {
		return failStage("start_transports", availabilityErr)
	}
	defer cleanup.teardown(func() {
		logCleanupFailure(ctx, processLogger, "drain_availability_server", availability.Drain())
	})()
	registrations, registrationErr := devicesnats.StartRegistrationServer(connection, validator, service, natsLogger)
	if registrationErr != nil {
		return failStage("start_transports", registrationErr)
	}
	defer cleanup.teardown(func() {
		logCleanupFailure(ctx, processLogger, "drain_registration_server", registrations.Drain())
	})()
	ownedMappings, ownedMappingsErr := devicesnats.StartOwnedMappingsServer(
		connection, validator, service, natsLogger,
	)
	if ownedMappingsErr != nil {
		return failStage("start_transports", ownedMappingsErr)
	}
	defer cleanup.teardown(func() {
		logCleanupFailure(ctx, processLogger, "drain_owned_mappings_server", ownedMappings.Drain())
	})()
	enablement, enablementErr := devicesnats.StartEntityEnablementServer(connection, validator, service, natsLogger)
	if enablementErr != nil {
		return failStage("start_transports", enablementErr)
	}
	defer cleanup.teardown(func() {
		logCleanupFailure(ctx, processLogger, "drain_enablement_server", enablement.Drain())
	})()
	logStartupStage(ctx, coreLogger, "nats_servers_started")
	if automationErr := automationFactConsumers.start(
		automationDurable,
		automationService,
		validator,
	); automationErr != nil {
		return mapStartupCancellation(ctx, failStage("start_automation_consumer", automationErr))
	}
	logStartupStage(ctx, coreLogger, "automation_consumer_started")
	if observationErr := consumers.startObservations(
		durable,
		validator,
		service,
		natsLogger,
	); observationErr != nil {
		return mapStartupCancellation(ctx, failStage("start_observation_consumer", observationErr))
	}
	logStartupStage(ctx, coreLogger, "observation_consumer_started")
	// Entity Events stop before the observation consumer, the NATS connection,
	// and SQLite, so a report is never committed after its dependencies close.
	// Unprocessed or unacknowledged events stay in the stream for the next Core
	// process.
	if entityEventErr := consumers.startEntityEvents(
		entityEventConsumer,
		validator,
		service,
		natsLogger,
	); entityEventErr != nil {
		return mapStartupCancellation(ctx, failStage("start_entity_event_consumer", entityEventErr))
	}
	logStartupStage(ctx, coreLogger, "entity_event_consumer_started")

	readiness := NewRuntimeReadiness(
		database, connection, js,
		consumers.observations, consumers.entityEvents, relay,
		automationFactConsumers,
	)
	healthSupervisor := startHealthSupervisor(dependencyContext, readiness, service, coreLogger)
	defer cleanup.teardown(healthSupervisor.Stop)()
	handler, _ := NewHTTPHandler(service, automationService, readiness, service, automationService)
	// Bind the socket explicitly so http_listening is only logged after the
	// address is actually held; a bind failure never produces that event.
	listener, listenErr := (&net.ListenConfig{}).Listen(ctx, "tcp", config.HTTPAddr)
	if listenErr != nil {
		return failStage("http_listen", listenErr)
	}
	coreLogger.InfoContext(
		ctx,
		"core HTTP listening",
		slog.String("event", "core.http_listening"),
		slog.String("http_addr", listener.Addr().String()),
	)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: httpReadHeaderTimeout}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()
	maintenance.Go(func() {
		pruneRetainedHistory(
			dependencyContext, service, automationService, coreLogger,
			config.EffectiveObservationRetention(), config.EffectiveAutomationHistoryRetention(),
			retentionPruneInterval,
		)
	})

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return failStage("serve_http", fmt.Errorf("serve HTTP: %w", err))
		}
		return nil
	case <-ctx.Done():
		return shutdownOnCancel(
			cleanup, healthSupervisor, server, consumers,
			enablement, ownedMappings, registrations, availability, sessions,
			relay, connection, processLogger,
		)
	}
}

// mapStartupCancellation returns [context.Canceled] when an explicit shutdown
// interrupts startup, preserving staged deadline and dependency failures.
func mapStartupCancellation(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return err
}

// closeAdmission stops every admission path before any worker is joined. It
// drains the automation Device Fact consumer so no new automatic admission
// enters, then closes automation admission, then closes device Command
// admission. Both gates close before [joinAdmittedExecution] waits, so no
// callback or request handler can register new work while admitted work drains.
// The ordered shutdown path and the deferred error-exit drain share it, and each
// step is idempotent.
func closeAdmission(
	automationService *automations.Service,
	automationConsumers *automationConsumers,
	deviceService *devices.Service,
) {
	automationConsumers.drain()
	automationService.StopAdmission()
	deviceService.StopCommandAdmission()
}

// joinAdmittedExecution joins Automation workers before device Command workers,
// because an Automation Step needs a device Command: Automation workers must
// finish before the Command workers they depend on. Both gates are already
// closed by [closeAdmission], so no worker can be registered during the waits.
func joinAdmittedExecution(
	automationService *automations.Service,
	deviceService *devices.Service,
) {
	_ = automationService.WaitRuns(context.Background())
	_ = deviceService.WaitCommands(context.Background())
}

// shutdownOnCancel stops new admission and drains admitted workers before
// stopping transports. It runs the shared [executionCleanup] first: it drains
// the automation Device Fact consumer, closes automation admission, then closes
// Command admission, then joins Automation workers before Command workers,
// because an Automation Step needs a Command, and only then cancels shared
// dependencies. The deferred error-exit cleanup runs the same once, so no step
// runs twice and health, transports, NATS, and SQLite are withdrawn only after
// both waits returned.
// The HTTP listener stays open during the drain so readiness keeps reporting
// draining (503) instead of dropping connections; the gates reject new work at
// the service layer. Handlers that already entered ExecuteCommand own detached
// workers that outlive request cancellation and are joined above with
// process-owned contexts. The five-second HTTP shutdown timeout only bounds
// listener shutdown after the waits; it never proves commands drained;
// WaitCommands does, beyond that timeout when an Operation deadline requires it.
// A connection that no request completed cannot be reclaimed inside that window,
// so an expired window force-closes it rather than failing the cancellation.
// That dependency cancellation cannot reach a durable consumer callback, which
// runs under its consumer lifecycle context canceled only after that consumer
// drains below.
func shutdownOnCancel(
	cleanup *executionCleanup,
	healthSupervisor *healthSupervisor,
	server *http.Server,
	consumers *coreConsumers,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	relay *devicesnats.DeviceFactRelay,
	connection *natsgo.Conn,
	logger *slog.Logger,
) error {
	cleanup.run()
	healthSupervisor.Stop()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := server.Shutdown(shutdownContext)
	shutdownCancel()
	// Shutdown only reclaims a connection the server has already seen go idle.
	// A client that connected without completing a request is indistinguishable
	// from a slow client, and net/http frees such a socket only after
	// ReadHeaderTimeout plus a poll interval, which cannot fit inside this
	// window. Force-close what remains instead of widening the bound, so a
	// cancellation still drains dependencies in the normal order and reports a
	// real failure only when the listener itself cannot be shut down.
	if shutdownErr != nil {
		if !errors.Is(shutdownErr, context.DeadlineExceeded) {
			return failStage("shutdown_http", fmt.Errorf("shutdown HTTP: %w", shutdownErr))
		}
		_ = server.Close()
	}
	return drainTransports(
		context.Background(), logger, consumers, relay, enablement, ownedMappings, registrations,
		availability, sessions, connection,
	)
}

// coreConsumers owns the two durable Core consumers and the lifecycle context
// their callbacks run under. Commands, health, and hourly maintenance share the
// dependency context, which shutdownOnCancel cancels before transports drain; a
// consumer callback that inherited that context would abort an already
// dispatched Observation projection or Entity Event record with
// [context.Canceled] instead of committing. Consumer callbacks therefore run
// under a context of their own, detached from process cancellation and canceled
// only after both consumers have drained or stopped on every exit, including a
// failed startup and a shutdown timeout.
type coreConsumers struct {
	callbackContext context.Context
	cancelCallbacks context.CancelFunc
	observations    *devicesnats.ObservationConsumer
	entityEvents    *devicesnats.EntityEventConsumer
}

// newCoreConsumers returns the lifecycle both durable consumers share. The
// callback context is detached from parent cancellation so a canceled process
// context never reaches an in-flight callback, and it is separate from every
// other dependency context so dependency teardown never does either.
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

// drain drains both durable consumers, Entity Events before Observations. It is
// the one drain order Core uses: unacknowledged reports remain for the next Core
// process instead of being acknowledged during teardown. A consumer that leaves
// nothing in flight closes promptly; a later close cancels its context. A
// consumer whose subscription never started is skipped: the embedded lifecycle
// is reached through a nil pointer before its own nil check can run.
func (consumers *coreConsumers) drain() {
	if consumers.entityEvents != nil {
		drainConsumer(consumers.entityEvents)
	}
	if consumers.observations != nil {
		drainConsumer(consumers.observations)
	}
}

// close ends both consumers and only then cancels the context their callbacks
// run under. Canceling first would abort an already-dispatched record with
// [context.Canceled]; draining first lets a callback that already entered
// persistence commit, and the bounded drain window still ends a callback that
// never returns. Anything unprocessed or unacknowledged when that window closes
// stays in the stream for the next Core process, and a startup failure before
// either subscription still leaves nothing running and nothing to stop.
func (consumers *coreConsumers) close() {
	consumers.drain()
	consumers.cancelCallbacks()
}

// consumerDrain is the shared lifecycle of a durable Core consumer: draining,
// stopping, and observing termination. Entity Event and Observation consumers
// implement it without sharing transport behavior.
type consumerDrain interface {
	Drain()
	Stop()
	Closed() <-chan struct{}
}

// drainTransports stops durable consumption and drains core transports after
// worker drain, preserving the existing return semantics for each step. Entity
// Events drain before Observation, and both before the relay. The relay then
// publishes every pending fact out of the durable outbox inside the shutdown
// deadline. That deadline is a bound, not a discard: rows still pending when it
// expires stay in the outbox for the next Core process, which always re-reads
// the oldest pending fact first. Core owns no fact consumer, so draining the
// relay never consumes its own publications. Transports drain next and the
// shared connection drains last, so no publication reaches a connection being
// torn down. The consumers' own context is canceled by coreConsumers.close
// after every exit, never here.
func drainTransports(
	ctx context.Context,
	logger *slog.Logger,
	consumers *coreConsumers,
	relay *devicesnats.DeviceFactRelay,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	connection *natsgo.Conn,
) error {
	consumers.drain()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
	drainErr := relay.Drain(drainContext)
	cancelDrain()
	logCleanupFailure(ctx, logger, "drain_device_facts", drainErr)
	for _, transport := range []interface{ Drain() error }{
		enablement, ownedMappings, registrations, availability, sessions,
	} {
		if err := transport.Drain(); err != nil {
			return err
		}
	}
	if err := connection.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		return fmt.Errorf("drain NATS connection: %w", err)
	}
	return nil
}

// drainConsumer drains one durable consumer and waits briefly for in-flight
// callbacks. Input that is still unprocessed or unacknowledged when the
// shutdown window closes stays in the stream; the consumer is then stopped
// rather than waited on indefinitely.
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

// logStartupStage emits one core.startup_stage_completed record for a finished
// startup step; failures return through failStage instead.
func logStartupStage(ctx context.Context, logger *slog.Logger, stage string) {
	logger.InfoContext(
		ctx,
		"core startup stage completed",
		slog.String("event", "core.startup_stage_completed"),
		slog.String("stage", stage),
	)
}

// logCleanupFailure records a failed deferred cleanup step without changing the
// caller's return semantics; successful cleanup stays silent.
func logCleanupFailure(ctx context.Context, logger *slog.Logger, stage string, err error) {
	if err == nil {
		return
	}
	logger.WarnContext(
		ctx,
		"process cleanup failed",
		slog.String("event", "process.cleanup_failed"),
		slog.String("stage", stage),
		slog.String("error_code", "cleanup_failed"),
	)
}

// connectCoreNATS opens the shared Core ingest/request connection. It keeps
// nats.go's default reconnect buffering and unlimited reconnects on the shared
// reconnect cadence, and it bounds one socket write by
// devicesnats.CoreNATSWriteTimeout: pinned nats.go v1.53.1 holds a connection's
// mutex across a socket write for up to its one-minute default FlusherTimeout,
// and both readiness and the Device Fact worker's per-fact freshness check read
// this connection synchronously, so an unbounded stalled write would hold
// shutdown past its five-second budget.
func connectCoreNATS(
	ctx context.Context,
	url string,
	logger *slog.Logger,
) (*natsgo.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, failStage("connect_nats", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(slog.String("dependency", "nats"))
	options := []natsgo.Option{
		natsgo.Name("hearthd"),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(natsReconnectWait),
		natsgo.FlusherTimeout(devicesnats.CoreNATSWriteTimeout),
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, disconnectErr error) {
			if disconnectErr == nil || ctx.Err() != nil {
				return
			}
			logger.WarnContext(
				ctx,
				"NATS disconnected",
				slog.String("event", "dependency.disconnected"),
				slog.String("error_code", "nats_disconnected"),
			)
		}),
		natsgo.ReconnectHandler(func(_ *natsgo.Conn) {
			if ctx.Err() != nil {
				return
			}
			logger.InfoContext(
				ctx, "NATS reconnected", slog.String("event", "dependency.reconnected"),
			)
		}),
		natsgo.ErrorHandler(func(_ *natsgo.Conn, _ *natsgo.Subscription, _ error) {
			if ctx.Err() != nil {
				return
			}
			logger.ErrorContext(ctx, "NATS operation failed",
				slog.String("event", "dependency.operation_failed"),
				slog.String("error_code", "nats_async_error"),
			)
		}),
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
			logger.DebugContext(
				ctx, "NATS connection closed", slog.String("event", "dependency.closed"),
			)
		}),
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, failStage("connect_nats", context.DeadlineExceeded)
		}
		options = append(options, natsgo.Timeout(remaining))
	}
	connection, err := natsgo.Connect(url, options...)
	if err != nil {
		return nil, failStage("connect_nats", fmt.Errorf("connect to NATS: %w", err))
	}
	logger.InfoContext(
		ctx, "NATS connected", slog.String("event", "dependency.connected"),
	)
	return connection, nil
}

// pruneRetainedHistory is the single hourly maintenance pass that bounds
// retained history. Entity Event history is pruned with the fixed internal
// EntityEventHistoryRetention window, not a Core setting, so the same pass
// serves both retentions without adding a timer. Each pass derives one sweep
// time; Service.DeleteExpiredEntityEvents then uses one cutoff strict-before
// that instant and deletes in bounded batches. Automation history uses the
// configured `automation_history_retention` window and the same sweep time, so
// a shorter window takes effect on the next pass. Startup never calls it, so
// uptime under one interval means no sweep has run yet.
func pruneRetainedHistory(
	ctx context.Context,
	service *devices.Service,
	automationService *automations.Service,
	logger *slog.Logger,
	observationRetention time.Duration,
	automationRetention time.Duration,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			sweepTime := now.UTC()
			if err := service.DeleteExpiredObservations(ctx, sweepTime, observationRetention); err != nil {
				logger.ErrorContext(
					ctx,
					"prune observations",
					slog.String("event", "core.observations_prune_failed"),
					slog.String("error_code", "observations_prune_failed"),
				)
			}
			if err := service.DeleteExpiredEntityEvents(ctx, sweepTime); err != nil {
				logger.ErrorContext(
					ctx,
					"prune entity events",
					slog.String("event", "core.entity_events_prune_failed"),
					slog.String("error_code", "entity_events_prune_failed"),
				)
			}
			pruneAutomationHistory(ctx, automationService, sweepTime, automationRetention)
		}
	}
}

// pruneAutomationHistory bounds terminal Automation Runs and Skips. The zero
// batch lets the automations service choose its own bounded batch size, active
// Runs and matched-Fact receipts are never selected, and the service records
// core.automation_history_prune_failed itself. A failed pass logs and retries
// next hour; it never fails readiness.
func pruneAutomationHistory(
	ctx context.Context,
	service *automations.Service,
	sweepTime time.Time,
	retention time.Duration,
) {
	_, _ = service.PruneHistory(ctx, sweepTime.Add(-retention), 0)
}

// drainExecution runs before any dependency teardown on every exit. It stops
// new admission, then joins already-admitted Automation workers and then device
// Command workers (including detached Commands whose HTTP handlers already
// returned) before canceling shared observation, health, and persistence
// dependencies. [executionCleanup] runs it exactly once and wraps every
// dependency teardown that registered after it, so the normal shutdown path and
// every error exit observe the same order.
func drainExecution(
	automationService *automations.Service,
	automationConsumers *automationConsumers,
	deviceService *devices.Service,
	cancelDependencies context.CancelFunc,
) {
	closeAdmission(automationService, automationConsumers, deviceService)
	joinAdmittedExecution(automationService, deviceService)
	cancelDependencies()
}

// executionCleanup is Core's ordered execution teardown, performed exactly once
// per process. It drains the automation Device Fact consumer, closes automation
// admission and then device Command admission, joins Automation workers and then
// Command workers, cancels the shared dependency context, and waits for hourly
// maintenance to return.
//
// Go runs deferred calls in reverse registration order. Run installs
// [executionCleanup.run] as a fallback defer after the database, NATS
// connection, consumer, and relay teardowns, so those dependencies are always
// withdrawn after it. Every teardown registered after the fallback is wrapped
// with [executionCleanup.teardown] instead, because a plain defer would
// otherwise stop health, drain the request/reply transports, and close what
// admitted work still needs before the gates closed and the workers joined. The
// ordered shutdown path calls the same run, so the once keeps a single wait and
// a single dependency cancellation on normal and error exits alike.
type executionCleanup struct {
	once                sync.Once
	automationService   *automations.Service
	automationConsumers *automationConsumers
	deviceService       *devices.Service
	cancelDependencies  context.CancelFunc
	maintenance         *sync.WaitGroup
}

// run performs the ordered execution teardown the first time any defer reaches
// it and is a no-op afterwards.
func (cleanup *executionCleanup) run() {
	cleanup.once.Do(func() {
		drainExecution(
			cleanup.automationService, cleanup.automationConsumers,
			cleanup.deviceService, cleanup.cancelDependencies,
		)
		cleanup.maintenance.Wait()
	})
}

// teardown wraps one health or dependency teardown so it can only withdraw its
// resource after the execution cleanup has run. It returns the deferred
// function, so a call site reads `defer cleanup.teardown(step)()`.
func (cleanup *executionCleanup) teardown(withdraw func()) func() {
	return func() {
		cleanup.run()
		withdraw()
	}
}
