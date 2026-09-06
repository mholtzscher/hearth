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

func Run(ctx context.Context, config Config, logger *slog.Logger) error { //nolint:funlen // Linear resource lifecycle.
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
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return failStage("interrupt_commands", fmt.Errorf("interrupt active commands: %w", err))
	}
	logStartupStage(ctx, coreLogger, "active_commands_interrupted")
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
	observations, observationErr := devicesnats.StartObservationConsumer(ctx, durable, validator, service, natsLogger)
	if observationErr != nil {
		return mapStartupCancellation(ctx, failStage("start_observation_consumer", observationErr))
	}
	defer observations.Stop()
	logStartupStage(ctx, coreLogger, "observation_consumer_started")

	readiness := NewRuntimeReadiness(database, connection, js, observations)
	healthSupervisor := startHealthSupervisor(ctx, readiness, service, coreLogger)
	defer healthSupervisor.Stop()
	handler, _ := NewHTTPHandler(service, readiness)
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
	go pruneObservations(ctx, service, coreLogger, config.EffectiveObservationRetention())

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return failStage("serve_http", fmt.Errorf("serve HTTP: %w", err))
		}
		return nil
	case <-ctx.Done():
		return shutdownCore(
			healthSupervisor, server, observations, enablement, ownedMappings, registrations, availability, sessions,
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

// shutdownCore stops lease-expiry supervision and drains core transports after
// cancellation, preserving the existing return semantics for each step.
func shutdownCore(
	healthSupervisor *healthSupervisor,
	server *http.Server,
	observations *devicesnats.ObservationConsumer,
	enablement, ownedMappings, registrations, availability, sessions interface{ Drain() error },
	connection *natsgo.Conn,
) error {
	healthSupervisor.Stop()
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return failStage("shutdown_http", fmt.Errorf("shutdown HTTP: %w", err))
	}
	observations.Drain()
	select {
	case <-observations.Closed():
	case <-shutdownContext.Done():
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
