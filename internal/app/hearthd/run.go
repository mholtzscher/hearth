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
	// Observation and Entity Event pruning run only on the hourly pass below, so
	// startup never sweeps retained history and uptime under one hour means no
	// sweep yet.

	connection, connectErr := connectCoreNATS(ctx, config.NATSURL, natsLogger)
	if connectErr != nil {
		return mapStartupCancellation(ctx, connectErr)
	}
	defer connection.Close()
	// The dedicated fact connection buffers nothing, so a publication attempted
	// while it is reconnecting fails instead of reaching a subscriber after
	// reconnect. The shared connection keeps its own buffering above.
	factConnection, factConnectErr := connectDeviceFactNATS(ctx, config.NATSURL, natsLogger)
	if factConnectErr != nil {
		return mapStartupCancellation(ctx, factConnectErr)
	}
	defer factConnection.Close()
	// One fence owns both connections' live windows. It is attached before the
	// dispatcher starts, so the first fact is already gated by both initial
	// generations and no backlog can be published as live.
	epochs, epochsErr := devicesnats.NewDeviceFactEpochs(connection, factConnection, time.Now)
	if epochsErr != nil {
		return failStage("connect_nats", epochsErr)
	}
	epochs.Track()
	validator, compileErr := contractsv1.Compile()
	if compileErr != nil {
		return failStage("compile_schemas", fmt.Errorf("compile wire schemas: %w", compileErr))
	}
	dispatcher, dispatcherErr := devicesnats.StartDeviceFactDispatcher(
		factConnection, validator, epochs, natsLogger,
	)
	if dispatcherErr != nil {
		return failStage("start_device_facts", dispatcherErr)
	}
	// The dispatcher drain runs after every transport that can commit a fact has
	// drained, and the dedicated connection outlives it.
	defer func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelDrain()
		logCleanupFailure(ctx, processLogger, "drain_device_facts", dispatcher.Drain(drainContext))
	}()
	commandSender := devicesnats.NewCommandSender(connection, validator)
	service := devices.NewService(
		devices.SQLiteStores(repository),
		commandSender,
		catalog,
		devices.Dependencies{Logger: devicesLogger, DeviceFacts: dispatcher},
	)
	js, jetStreamErr := jetstream.New(connection)
	if jetStreamErr != nil {
		return failStage(
			"provision_jetstream",
			fmt.Errorf("create JetStream client: %w", jetStreamErr),
		)
	}
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
	logStartupStage(ctx, coreLogger, "jetstream_provisioned")
	sessions, sessionErr := devicesnats.StartSessionServer(connection, validator, service, service, natsLogger)
	if sessionErr != nil {
		return failStage("start_transports", sessionErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "drain_session_server", sessions.Drain())
	}()
	availability, availabilityErr := devicesnats.StartEntityAvailabilityServer(
		connection, validator, service, natsLogger,
	)
	if availabilityErr != nil {
		return failStage("start_transports", availabilityErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "drain_availability_server", availability.Drain())
	}()
	registrations, registrationErr := devicesnats.StartRegistrationServer(connection, validator, service, natsLogger)
	if registrationErr != nil {
		return failStage("start_transports", registrationErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "drain_registration_server", registrations.Drain())
	}()
	ownedMappings, ownedMappingsErr := devicesnats.StartOwnedMappingsServer(
		connection, validator, service, natsLogger,
	)
	if ownedMappingsErr != nil {
		return failStage("start_transports", ownedMappingsErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "drain_owned_mappings_server", ownedMappings.Drain())
	}()
	enablement, enablementErr := devicesnats.StartEntityEnablementServer(connection, validator, service, natsLogger)
	if enablementErr != nil {
		return failStage("start_transports", enablementErr)
	}
	defer func() {
		logCleanupFailure(ctx, processLogger, "drain_enablement_server", enablement.Drain())
	}()
	logStartupStage(ctx, coreLogger, "nats_servers_started")
	// Current command workers must retain observation and health dependencies
	// beyond shutdown cancellation, including on error exits.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelDependencies()
	// Durable consumer callbacks never share dependencyContext: it is canceled
	// before transports drain, which would abort a report or observation that
	// already entered SQLite with context.Canceled. The consumers own a detached
	// lifecycle context that is canceled only after both have drained or
	// stopped, so an already dispatched callback always reaches its commit.
	consumers := newCoreConsumers(ctx)
	defer consumers.close()
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
		database, connection, factConnection, js,
		consumers.observations, consumers.entityEvents, dispatcher,
	)
	healthSupervisor := startHealthSupervisor(dependencyContext, readiness, service, coreLogger)
	defer healthSupervisor.Stop()
	var maintenance sync.WaitGroup
	// Registered after dependency cleanup so every exit drains workers first.
	defer func() {
		drainExecution(service, cancelDependencies)
		maintenance.Wait()
	}()
	handler, _ := NewHTTPHandler(service, readiness, service)
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
			dependencyContext, service, coreLogger,
			config.EffectiveObservationRetention(), retentionPruneInterval,
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
			service, cancelDependencies,
			healthSupervisor, server, consumers,
			enablement, ownedMappings, registrations, availability, sessions,
			dispatcher, connection, factConnection, processLogger,
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

// joinAdmittedExecution closes admission before joining workers, so
// no new Command can be registered while draining. Both
// the normal shutdown path and the deferred error-exit drain share this
// ordering.
func joinAdmittedExecution(deviceService *devices.Service) {
	deviceService.StopCommandAdmission()
	_ = deviceService.WaitCommands(context.Background())
}

// shutdownOnCancel drains admitted workers before stopping transports. The HTTP
// listener stays open during the drain so readiness keeps reporting draining
// (503) instead of dropping connections; the gates reject new work at the
// service layer. Handlers that already entered ExecuteCommand own detached
// workers that outlive request cancellation and are joined below with
// process-owned contexts. The five-second HTTP shutdown timeout only bounds
// listener shutdown after the waits; it never proves commands drained;
// WaitCommands does, beyond that timeout when an Operation deadline requires
// it. A connection that no request completed cannot be reclaimed inside that
// window, so an expired window force-closes it rather than failing the
// cancellation. Dependencies stay alive until both waits return and are
// canceled only then, on both normal and error exits (error exits reuse
// drainExecution through the deferred cleanup). That cancellation cannot reach
// a durable consumer callback, which runs under the consumer lifecycle context
// canceled only after both consumers drain below.
func shutdownOnCancel(
	deviceService *devices.Service,
	cancelDependencies context.CancelFunc,
	healthSupervisor *healthSupervisor,
	server *http.Server,
	consumers *coreConsumers,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	dispatcher *devicesnats.DeviceFactDispatcher,
	connection *natsgo.Conn,
	factConnection *natsgo.Conn,
	logger *slog.Logger,
) error {
	joinAdmittedExecution(deviceService)
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
	cancelDependencies()
	return drainTransports(
		context.Background(), logger, consumers, dispatcher, enablement, ownedMappings, registrations,
		availability, sessions, connection, factConnection,
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
// Events drain before Observation, and both before the dispatcher, so facts for
// work that already committed still reach live subscribers. The dispatcher then
// drains its bounded queue with the shutdown deadline; on a timeout it closes
// the dedicated fact connection itself, discards the queue and joins its worker,
// and that loss is recorded without failing an otherwise clean shutdown. The
// shared and dedicated connections drain last, so no publication reaches a
// connection being torn down. The consumers' own context is canceled by
// coreConsumers.close after every exit, never here.
func drainTransports(
	ctx context.Context,
	logger *slog.Logger,
	consumers *coreConsumers,
	dispatcher *devicesnats.DeviceFactDispatcher,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	connection *natsgo.Conn,
	factConnection *natsgo.Conn,
) error {
	consumers.drain()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), shutdownTimeout)
	drainErr := dispatcher.Drain(drainContext)
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
	factConnection.Close()
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

// connectDeviceFactNATS opens the dedicated Device Fact publication connection:
// a distinct client name, unlimited reconnects and no reconnect buffering, so a
// publication attempted while the connection is reconnecting fails and is
// dropped rather than delivered later. Its diagnostics mirror the shared Core
// connection's, tagged with the connection they belong to.
func connectDeviceFactNATS(
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
	logger = logger.With(slog.String("dependency", "nats_device_facts"))
	const connectionAttribute = "device_facts"
	options := devicesnats.DeviceFactConnectionOptions()
	options = append(options,
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, disconnectErr error) {
			if disconnectErr == nil || ctx.Err() != nil {
				return
			}
			logger.WarnContext(
				ctx,
				"device fact NATS disconnected",
				slog.String("event", "dependency.disconnected"),
				slog.String("connection", connectionAttribute),
				slog.String("error_code", "nats_disconnected"),
			)
		}),
		natsgo.ReconnectHandler(func(_ *natsgo.Conn) {
			if ctx.Err() != nil {
				return
			}
			logger.InfoContext(
				ctx, "device fact NATS reconnected",
				slog.String("event", "dependency.reconnected"),
				slog.String("connection", connectionAttribute),
			)
		}),
		natsgo.ErrorHandler(func(_ *natsgo.Conn, _ *natsgo.Subscription, _ error) {
			if ctx.Err() != nil {
				return
			}
			logger.ErrorContext(ctx, "device fact NATS operation failed",
				slog.String("event", "dependency.operation_failed"),
				slog.String("connection", connectionAttribute),
				slog.String("error_code", "nats_async_error"),
			)
		}),
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
			logger.DebugContext(
				ctx, "device fact NATS connection closed",
				slog.String("event", "dependency.closed"),
				slog.String("connection", connectionAttribute),
			)
		}),
	)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, failStage("connect_nats", context.DeadlineExceeded)
		}
		options = append(options, natsgo.Timeout(remaining))
	}
	connection, err := natsgo.Connect(url, options...)
	if err != nil {
		return nil, failStage("connect_nats", fmt.Errorf("connect device fact NATS: %w", err))
	}
	logger.InfoContext(
		ctx, "device fact NATS connected",
		slog.String("event", "dependency.connected"),
		slog.String("connection", connectionAttribute),
	)
	return connection, nil
}

// pruneRetainedHistory is the single hourly maintenance pass that bounds
// retained history. Entity Event history is pruned with the fixed internal
// EntityEventHistoryRetention window, not a Core setting, so the same pass
// serves both retentions without adding a timer. Each pass derives one sweep
// time; Service.DeleteExpiredEntityEvents then uses one cutoff strict-before
// that instant and deletes in bounded batches. Startup never calls it, so
// uptime under one interval means no sweep has run yet.
func pruneRetainedHistory(
	ctx context.Context,
	service *devices.Service,
	logger *slog.Logger,
	observationRetention time.Duration,
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
		}
	}
}

// drainExecution runs before any dependency teardown on every exit. It joins
// already-admitted workers (including detached Commands whose HTTP handlers
// already returned) before canceling shared observation, health, and
// persistence dependencies.
func drainExecution(
	deviceService *devices.Service,
	cancelDependencies context.CancelFunc,
) {
	joinAdmittedExecution(deviceService)
	cancelDependencies()
}
