package hearthd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const (
	shutdownTimeout       = 5 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	natsReconnectWait     = 250 * time.Millisecond
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

//nolint:funlen,gocognit // Linear startup keeps dependency order explicit.
func Run(
	ctx context.Context,
	config Config,
	logger *slog.Logger,
) (runErr error) {
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
	shutdown := &coreShutdown{runContext: ctx, logger: processLogger}
	// Register resources as they start so this defer also handles partial startup.
	// Preserve the original failure; shutdown logs any cleanup failures.
	defer func() {
		if shutdownErr := shutdown.run(); shutdownErr != nil && runErr == nil {
			runErr = shutdownErr
		}
	}()
	catalog, catalogErr := devices.NewBuiltinTypeCatalog()
	if catalogErr != nil {
		return failStage("load_type_catalog", catalogErr)
	}
	database, openErr := platformdb.Open(ctx, config.SQLitePath)
	if openErr != nil {
		return failStage("open_database", openErr)
	}
	shutdown.database = database
	if err := platformdb.Migrate(ctx, database); err != nil {
		return failStage("migrate_database", err)
	}
	logStartupStage(ctx, coreLogger, "database_migrated")
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	startupTime := time.Now().UTC()
	// Interrupted records are committed before either transport opens; their
	// Command history is authoritative and no Device Fact is published for Core's
	// own startup interruption.
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return failStage("interrupt_commands", fmt.Errorf("interrupt active commands: %w", err))
	}
	logStartupStage(ctx, coreLogger, "active_commands_interrupted")
	// Interrupt Automation Runs before opening transports, so stale Runs cannot
	// advance. The service is constructed later; recovery needs only the repository.
	automationRepository := automationssqlite.NewAutomationRepository(
		database, automations.AutomationDependencies{},
	)
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

	connection, connectErr := connectCoreNATS(ctx, config.NATSURL, natsLogger)
	if connectErr != nil {
		return mapStartupCancellation(ctx, connectErr)
	}
	shutdown.connection = connection
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
	// Start the outbox relay before fact producers. At shutdown it drains after
	// the consumers, while NATS and SQLite are still available.
	relay, relayErr := devicesnats.StartDeviceFactRelay(js, repository, validator, natsLogger)
	if relayErr != nil {
		return failStage("start_device_facts", relayErr)
	}
	shutdown.relay = relay
	commandSender := devicesnats.NewCommandSender(connection, validator)
	service := devices.NewService(
		devicessqlite.DeviceStores(repository),
		commandSender,
		catalog,
		devices.Dependencies{
			Logger:      devicesLogger,
			DeviceFacts: relay,
			// Retention policy is injected once here; the shared history pruning
			// worker calls PruneHistory with a sweep time and no window.
			ObservationRetention: config.EffectiveObservationRetention(),
		},
	)
	shutdown.deviceService = service
	// The devices service implements the read-only devices-facing seam the
	// automations module consumes, so construction order is devices first.
	automationService := automations.NewService(
		automationRepository,
		service,
		automations.AutomationDependencies{
			Logger:           automationsLogger,
			HistoryRetention: config.EffectiveAutomationHistoryRetention(),
		},
	)
	shutdown.automationService = automationService
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
	// Keep Command dependencies alive until admitted workers finish. Consumers
	// own separate contexts because their callbacks drain after these dependencies.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	shutdown.cancelDependencies = cancelDependencies
	// Startup recovery has already interrupted stale Commands and Runs, so the
	// shared retention worker can start. Its first pass runs inside the worker,
	// not before readiness or serving, and later passes run hourly until
	// shutdown joins the worker before SQLite closes.
	shutdown.historyPruneWorker = startHistoryPruning(
		dependencyContext, coreLogger, service, automationService,
	)
	consumers := newCoreConsumers(ctx)
	shutdown.consumers = consumers
	// Start automatic admission before inbound Fact producers. A new consumer
	// starts at the tail; an existing one resumes its durable acknowledgement floor.
	automationFactConsumers := newAutomationConsumers(ctx, automationsLogger)
	shutdown.automationConsumers = automationFactConsumers
	sessions, sessionErr := devicesnats.StartSessionServer(connection, validator, service, service, natsLogger)
	if sessionErr != nil {
		return failStage("start_transports", sessionErr)
	}
	shutdown.transports = append(shutdown.transports, registeredDrain{
		stage: "drain_session_server", drain: sessions,
	})
	availability, availabilityErr := devicesnats.StartEntityAvailabilityServer(
		connection, validator, service, natsLogger,
	)
	if availabilityErr != nil {
		return failStage("start_transports", availabilityErr)
	}
	shutdown.transports = append(shutdown.transports, registeredDrain{
		stage: "drain_availability_server", drain: availability,
	})
	registrations, registrationErr := devicesnats.StartRegistrationServer(connection, validator, service, natsLogger)
	if registrationErr != nil {
		return failStage("start_transports", registrationErr)
	}
	shutdown.transports = append(shutdown.transports, registeredDrain{
		stage: "drain_registration_server", drain: registrations,
	})
	ownedMappings, ownedMappingsErr := devicesnats.StartOwnedMappingsServer(
		connection, validator, service, natsLogger,
	)
	if ownedMappingsErr != nil {
		return failStage("start_transports", ownedMappingsErr)
	}
	shutdown.transports = append(shutdown.transports, registeredDrain{
		stage: "drain_owned_mappings_server", drain: ownedMappings,
	})
	enablement, enablementErr := devicesnats.StartEntityEnablementServer(connection, validator, service, natsLogger)
	if enablementErr != nil {
		return failStage("start_transports", enablementErr)
	}
	shutdown.transports = append(shutdown.transports, registeredDrain{
		stage: "drain_enablement_server", drain: enablement,
	})
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
	shutdown.healthSupervisor = healthSupervisor
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
	shutdown.server = server
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return failStage("serve_http", fmt.Errorf("serve HTTP: %w", err))
		}
		return nil
	case <-ctx.Done():
		// Deferred shutdown keeps HTTP serving draining readiness until workers finish.
		return nil
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

// connectCoreNATS opens Core's shared connection with unlimited reconnects.
// Bound socket writes: nats.go holds its connection mutex during writes, so a
// stalled write would also block readiness, Fact freshness checks, and shutdown.
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
