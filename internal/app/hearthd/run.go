package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const shutdownTimeout = 5 * time.Second

type appShutdownStep uint8

const (
	shutdownIngressQuiesced appShutdownStep = iota + 1
	shutdownModuleCanceled
	shutdownModuleJoined
	shutdownIngressJoined
	shutdownNATSClosed
	shutdownDatabaseClosed
)

type appControls struct {
	beforeRegistrationStart func() error
	beforeObservationStart  func() error
	serveHTTP               func(*http.Server) error
	onShutdownStep          func(appShutdownStep)
}

func productionAppControls() appControls {
	return appControls{
		beforeRegistrationStart: func() error { return nil },
		beforeObservationStart:  func() error { return nil },
		serveHTTP:               func(server *http.Server) error { return server.ListenAndServe() },
		onShutdownStep:          func(appShutdownStep) {},
	}
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	return run(ctx, config, logger, productionAppControls())
}

func run(ctx context.Context, config Config, logger *slog.Logger, controls appControls) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	controls = completeAppControls(controls)

	database, err := platformdb.Open(ctx, config.SQLitePath)
	if err != nil {
		return err
	}
	if err := platformdb.Migrate(ctx, database); err != nil {
		return joinRunErrors(err, database.Close())
	}
	service, err := devices.New(ctx, database, logger)
	if err != nil {
		return joinRunErrors(err, database.Close())
	}

	connection, err := connectCoreNATS(ctx, config.NATSURL)
	if err != nil {
		return joinRunErrors(err, database.Close())
	}
	closeBeforeRun := func(primary error) error {
		connection.Close()
		controls.onShutdownStep(shutdownNATSClosed)
		databaseError := database.Close()
		controls.onShutdownStep(shutdownDatabaseClosed)
		return joinRunErrors(primary, databaseError)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		return closeBeforeRun(fmt.Errorf("create JetStream client: %w", err))
	}
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		return closeBeforeRun(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return closeBeforeRun(fmt.Errorf("compile wire schemas: %w", err))
	}
	delivery := devicesnats.NewCommandDelivery(platformnats.NewCommandClient(connection, validator))
	moduleContext, cancelModule := context.WithCancel(context.Background())
	moduleErrors := make(chan error, 1)
	go func() { moduleErrors <- service.Run(moduleContext, delivery) }()

	var registrations *platformnats.RegistrationServer
	var observations *platformnats.ObservationConsumer
	var server *http.Server
	var serverErrors chan error
	cleanup := func(primary error) error {
		return cleanupRuntime(
			primary, controls, cancelModule, moduleErrors, registrations, observations,
			server, serverErrors, connection, database,
		)
	}

	if err := controls.beforeRegistrationStart(); err != nil {
		return cleanup(err)
	}
	registrations, err = platformnats.StartRegistrationServer(
		connection, validator, devicesnats.RegistrationHandler(service), logger,
	)
	if err != nil {
		return cleanup(err)
	}
	if err := controls.beforeObservationStart(); err != nil {
		return cleanup(err)
	}
	observations, err = platformnats.StartObservationConsumer(
		ctx, durable, validator, devicesnats.ObservationHandler(service), logger,
	)
	if err != nil {
		return cleanup(err)
	}

	readiness := NewRuntimeReadiness(database, connection, js, observations)
	handler, _ := NewHTTPHandler(devicesapi.Dependencies{Entities: service, Commands: service}, readiness)
	server = &http.Server{Addr: config.HTTPAddr, Handler: handler}
	serverErrors = make(chan error, 1)
	go func() { serverErrors <- controls.serveHTTP(server) }()

	select {
	case err := <-serverErrors:
		serverErrors = nil
		if !errors.Is(err, http.ErrServerClosed) {
			return cleanup(fmt.Errorf("serve HTTP: %w", err))
		}
		return cleanup(nil)
	case <-ctx.Done():
		return cleanup(nil)
	}
}

func cleanupRuntime(
	primary error,
	controls appControls,
	cancelModule context.CancelFunc,
	moduleErrors <-chan error,
	registrations *platformnats.RegistrationServer,
	observations *platformnats.ObservationConsumer,
	server *http.Server,
	serverErrors <-chan error,
	connection *natsgo.Conn,
	database *sql.DB,
) error {
	var cleanup []error
	var httpShutdown <-chan error
	if server != nil {
		completed := make(chan error, 1)
		httpShutdown = completed
		go func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			completed <- server.Shutdown(shutdownContext)
		}()
	}

	registrationClosed := registrations.Closed()
	observationClosed := observations.Closed()
	if registrations != nil {
		cleanup = append(cleanup, registrations.Drain())
	}
	if observations != nil {
		observations.Drain()
	}
	controls.onShutdownStep(shutdownIngressQuiesced)

	cancelModule()
	controls.onShutdownStep(shutdownModuleCanceled)
	if err := <-moduleErrors; err != nil {
		cleanup = append(cleanup, err)
	}
	controls.onShutdownStep(shutdownModuleJoined)

	<-registrationClosed
	<-observationClosed
	if httpShutdown != nil {
		if err := <-httpShutdown; err != nil && !errors.Is(err, http.ErrServerClosed) {
			cleanup = append(cleanup, err)
		}
		if serverErrors != nil {
			if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
				cleanup = append(cleanup, err)
			}
		}
	}
	controls.onShutdownStep(shutdownIngressJoined)

	if err := connection.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		cleanup = append(cleanup, fmt.Errorf("drain NATS connection: %w", err))
	}
	connection.Close()
	controls.onShutdownStep(shutdownNATSClosed)
	cleanup = append(cleanup, database.Close())
	controls.onShutdownStep(shutdownDatabaseClosed)
	return joinRunErrors(primary, cleanup...)
}

func completeAppControls(controls appControls) appControls {
	production := productionAppControls()
	if controls.beforeRegistrationStart == nil {
		controls.beforeRegistrationStart = production.beforeRegistrationStart
	}
	if controls.beforeObservationStart == nil {
		controls.beforeObservationStart = production.beforeObservationStart
	}
	if controls.serveHTTP == nil {
		controls.serveHTTP = production.serveHTTP
	}
	if controls.onShutdownStep == nil {
		controls.onShutdownStep = production.onShutdownStep
	}
	return controls
}

func joinRunErrors(primary error, cleanup ...error) error {
	return errors.Join(append([]error{primary}, cleanup...)...)
}

func connectCoreNATS(ctx context.Context, url string) (*natsgo.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := []natsgo.Option{
		natsgo.Name("hearthd"),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(250 * time.Millisecond),
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		options = append(options, natsgo.Timeout(remaining))
	}
	connection, err := natsgo.Connect(url, options...)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	return connection, nil
}
