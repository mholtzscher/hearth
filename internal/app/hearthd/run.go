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
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

const (
	observationPruneInterval = time.Hour
	shutdownTimeout          = 5 * time.Second
	httpReadHeaderTimeout    = 5 * time.Second
	natsReconnectWait        = 250 * time.Millisecond
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

// RunOption customizes process assembly without broadening Config. Only the
// automation scheduler loop clock and wakeup are exposed, so later embedded-NATS
// scheduling tests can share one fake time source between the repository final
// check and the scheduler loop. Production passes no options.
type RunOption func(*runOptions)

type runOptions struct {
	schedulerClock  func() time.Time
	schedulerWakeup <-chan struct{}
	// shutdownOrderProbe records the process shutdown order for tests, one
	// entry per shutdown order step. Nil in production, where no step is
	// recorded.
	shutdownOrderProbe func(step string)
}

// WithSchedulerClock overrides the scheduler loop clock and the repository
// scheduler final-check clock with the same source, so ticks and the commit
// check agree on time. A nil clock keeps the default wall-clock behavior.
func WithSchedulerClock(clock func() time.Time) RunOption {
	return func(options *runOptions) {
		options.schedulerClock = clock
	}
}

// WithSchedulerWakeup injects a wakeup channel that fully replaces the
// one-second scheduler process timer. A nil channel keeps production timing.
func WithSchedulerWakeup(wakeup <-chan struct{}) RunOption {
	return func(options *runOptions) {
		options.schedulerWakeup = wakeup
	}
}

// automationRepositoryOptions aligns the repository scheduler final-check clock
// with the loop clock from the same source, so ticks and the commit check
// agree on time. No option keeps the repository default.
func (options runOptions) automationRepositoryOptions() []automations.SQLiteRepositoryOption {
	if options.schedulerClock == nil {
		return nil
	}
	return []automations.SQLiteRepositoryOption{
		automations.WithAutomationSchedulerClock(options.schedulerClock),
	}
}

// automationServiceOptions forwards the narrow scheduler loop seams. No option
// keeps the production one-second timer and wall clock.
func (options runOptions) automationServiceOptions() []automations.AutomationServiceOption {
	var serviceOptions []automations.AutomationServiceOption
	if options.schedulerClock != nil {
		serviceOptions = append(serviceOptions, automations.WithSchedulerClock(options.schedulerClock))
	}
	if options.schedulerWakeup != nil {
		serviceOptions = append(serviceOptions, automations.WithSchedulerWakeup(options.schedulerWakeup))
	}
	return serviceOptions
}

//nolint:funlen,gocognit // Linear lifecycle keeps drain and teardown order explicit.
func Run(
	ctx context.Context,
	config Config,
	logger *slog.Logger,
	options ...RunOption,
) error {
	timezone, configErr := config.validateAndLoadHouseholdTimezone()
	if configErr != nil {
		return failStage("validate_config", configErr)
	}
	var assembled runOptions
	for _, option := range options {
		if option != nil {
			option(&assembled)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	definitions, definitionErr := automations.NewAutomationDefinitionCodec()
	if definitionErr != nil {
		return failStage("compile_automation_schema", definitionErr)
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
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return failStage("interrupt_commands", fmt.Errorf("interrupt active commands: %w", err))
	}
	logStartupStage(ctx, coreLogger, "active_commands_interrupted")
	automationRepositoryOptions := assembled.automationRepositoryOptions()
	automationRepository := automations.NewSQLiteRepository(database, automationRepositoryOptions...)
	if err := automationRepository.InterruptAutomationRuns(ctx); err != nil {
		return failStage("interrupt_automation_runs", err)
	}
	logStartupStage(ctx, coreLogger, "active_automation_runs_interrupted")
	// Observation pruning runs only on the hourly pass below, so startup never
	// sweeps retained history and uptime under one hour means no sweep yet.

	connection, connectErr := connectCoreNATS(ctx, config.NATSURL, natsLogger)
	if connectErr != nil {
		return mapStartupCancellation(ctx, connectErr)
	}
	defer connection.Close()
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
	logStartupStage(ctx, coreLogger, "jetstream_provisioned")
	validator, compileErr := contractsv1.Compile()
	if compileErr != nil {
		return failStage("compile_schemas", fmt.Errorf("compile wire schemas: %w", compileErr))
	}
	commandSender := devicesnats.NewCommandSender(connection, validator)
	service := devices.NewService(
		devices.SQLiteStores(repository),
		commandSender,
		catalog,
		devices.Dependencies{Logger: devicesLogger},
	)

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
	// Current automation Commands must retain observation and health dependencies
	// beyond shutdown cancellation, including on error exits.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelDependencies()
	observations, observationErr := devicesnats.StartObservationConsumer(
		dependencyContext,
		durable,
		validator,
		service,
		natsLogger,
	)
	if observationErr != nil {
		return mapStartupCancellation(ctx, failStage("start_observation_consumer", observationErr))
	}
	defer observations.Stop()
	logStartupStage(ctx, coreLogger, "observation_consumer_started")

	automationService := automations.NewService(
		automationRepository,
		service,
		repository,
		definitions,
		timezone,
		logger.With(slog.String("component", "automations")),
		assembled.automationServiceOptions()...,
	)
	var maintenance sync.WaitGroup
	// Single shutdown order for every exit: close Run and next-Step admission
	// and join the scheduler before stopping health supervision, then cancel
	// shared dependencies. Registered after the observation and transport
	// cleanup above, so those defers run later and health plus observations
	// stay alive until admitted execution drains. Registered before scheduler
	// start so a scheduler start failure still closes admission; the supervisor
	// assignment below fills in before any later exit can observe it.
	// shutdownOnCancel reuses drainAdmittedExecution, so the deferred rerun
	// after a normal cancel is a no-op instead of a second scheduler join.
	var healthSupervisor *healthSupervisor
	var drainOnce sync.Once
	recordShutdownOrderStep := func(step string) {
		if assembled.shutdownOrderProbe != nil {
			assembled.shutdownOrderProbe(step)
		}
	}
	drainAdmittedExecution := func() {
		drainOnce.Do(func() {
			joinAdmittedExecution(service, automationService)
			recordShutdownOrderStep("execution drained")
			if healthSupervisor != nil {
				healthSupervisor.Stop()
				recordShutdownOrderStep("health supervision stopped")
			}
		})
	}
	defer func() {
		drainAdmittedExecution()
		cancelDependencies()
		recordShutdownOrderStep("dependencies canceled")
		maintenance.Wait()
	}()
	// Scheduler progress initializes synchronously after dependencies and
	// recovery, before readiness. A failure exits through the drain above,
	// which closes admission and joins the loop before worker drain.
	if err := automationService.StartAutomationScheduler(ctx); err != nil {
		return mapStartupCancellation(ctx, failStage("start_automation_scheduler", err))
	}
	logStartupStage(ctx, coreLogger, "automation_scheduler_started")
	readiness := NewRuntimeReadiness(database, connection, js, observations, automationService)
	healthSupervisor = startHealthSupervisor(dependencyContext, readiness, service, coreLogger)
	handler, _ := NewHTTPHandler(service, automationService, definitions, readiness, service)
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
		pruneObservations(dependencyContext, service, coreLogger, config.EffectiveObservationRetention())
	})
	maintenance.Go(func() {
		pruneAutomationHistory(
			dependencyContext,
			automationService,
			coreLogger,
			config.EffectiveAutomationHistoryRetention(),
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
			drainAdmittedExecution, cancelDependencies,
			server, observations, enablement, ownedMappings, registrations, availability, sessions,
			connection,
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

// joinAdmittedExecution closes both admission gates before joining workers, so
// no new Run, Step, or direct Command can be registered while draining. The
// scheduler loop stops and joins after gate closure and before the waits, so
// ticks racing shutdown evaluate nothing; already-registered workers drain
// through the waits below. Both the normal shutdown path and the deferred
// error-exit drain share this ordering.
func joinAdmittedExecution(deviceService *devices.Service, automationService *automations.Service) {
	deviceService.StopCommandAdmission()
	automationService.StopAutomationExecutionAdmission()
	automationService.StopAutomationScheduler()
	_ = automationService.WaitAutomationRuns(context.Background())
	_ = deviceService.WaitCommands(context.Background())
}

// shutdownOnCancel drains admitted workers through the shared once-guarded
// shutdown order before stopping transports. The HTTP
// listener stays open during the drain so readiness keeps reporting draining
// (503) instead of dropping connections; the gates reject new work at the
// service layer. Handlers that already entered ExecuteCommand own detached
// workers that outlive request cancellation and are joined below with
// process-owned contexts. The five-second HTTP shutdown timeout only bounds
// listener shutdown after the waits; it never proves commands drained;
// WaitCommands does, beyond that timeout when an Operation deadline requires
// it. Dependencies stay alive until both waits return and are canceled only
// then, on both normal and error exits (error exits reuse
// drainAdmittedExecution through the deferred cleanup, whose rerun after this
// call is a no-op instead of a second scheduler join).
func shutdownOnCancel(
	drainAdmittedExecution func(),
	cancelDependencies context.CancelFunc,
	server *http.Server,
	observations *devicesnats.ObservationConsumer,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	connection *natsgo.Conn,
) error {
	drainAdmittedExecution()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := server.Shutdown(shutdownContext)
	shutdownCancel()
	if shutdownErr != nil {
		return failStage("shutdown_http", fmt.Errorf("shutdown HTTP: %w", shutdownErr))
	}
	cancelDependencies()
	return drainTransports(observations, enablement, ownedMappings, registrations, availability, sessions, connection)
}

// drainTransports stops observation consumption and drains core transports after
// worker drain, preserving the existing return semantics for each step.
func drainTransports(
	observations *devicesnats.ObservationConsumer,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	connection *natsgo.Conn,
) error {
	observations.Drain()
	drainContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	select {
	case <-observations.Closed():
	case <-drainContext.Done():
		observations.Stop()
	}
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

func pruneObservations(
	ctx context.Context,
	service *devices.Service,
	logger *slog.Logger,
	retention time.Duration,
) {
	ticker := time.NewTicker(observationPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := service.DeleteExpiredObservations(ctx, now.UTC(), retention); err != nil {
				logger.ErrorContext(
					ctx,
					"prune observations",
					slog.String("event", "core.observations_prune_failed"),
					slog.String("error_code", "observations_prune_failed"),
				)
			}
		}
	}
}

// drainExecution joins already-admitted automation and direct workers
// (including detached direct Commands whose HTTP handlers already returned)
// before canceling shared observation, health, and persistence dependencies.
// App tests exercise this join-before-cancel shutdown order directly; the Run
// lifecycle above reuses joinAdmittedExecution with the same ordering and
// additionally stops health supervision between the join and the cancel.
func drainExecution(
	deviceService *devices.Service,
	automationService *automations.Service,
	cancelDependencies context.CancelFunc,
) {
	joinAdmittedExecution(deviceService, automationService)
	cancelDependencies()
}

func pruneAutomationHistory(
	ctx context.Context,
	service *automations.Service,
	logger *slog.Logger,
	retention time.Duration,
) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := service.PruneAutomationHistory(ctx, now.UTC().Add(-retention)); err != nil && ctx.Err() == nil {
				logger.ErrorContext(
					ctx,
					"prune automation history",
					slog.String("event", "core.automation_history_prune_failed"),
					slog.String("error_code", "automation_history_prune_failed"),
				)
			}
		}
	}
}
