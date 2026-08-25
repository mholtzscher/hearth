package hearthd

import (
	"context"
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
	if err := validateAppControls(controls); err != nil {
		return err
	}

	if ctx.Err() != nil {
		return nil
	}
	database, err := platformdb.Open(ctx, config.SQLitePath)
	if err != nil {
		return startupRunError(ctx, err)
	}
	if err := platformdb.Migrate(ctx, database); err != nil {
		return joinRunErrors(startupRunError(ctx, err), database.Close())
	}
	service, err := devices.New(ctx, database, logger)
	if err != nil {
		return joinRunErrors(startupRunError(ctx, err), database.Close())
	}

	var connection *natsgo.Conn
	var registrations *platformnats.RegistrationServer
	var observations *platformnats.ObservationConsumer
	var server *http.Server
	var moduleCancel context.CancelFunc
	var moduleErrors chan error
	var serverErrors chan error
	serverResultConsumed := false

	cleanup := func(primary error) error {
		var ingressErrors []error
		var moduleCleanupErrors []error
		var joinErrors []error
		var natsErrors []error
		var databaseErrors []error

		registrationClosed := closedChannel()
		if registrations != nil {
			registrationClosed = registrations.Closed()
		}
		observationClosed := closedChannel()
		if observations != nil {
			observationClosed = observations.Closed()
			observations.Drain()
		}
		if registrations != nil {
			if err := registrations.Drain(); err != nil {
				ingressErrors = append(ingressErrors, err)
			}
		}

		httpShutdown := make(chan error, 1)
		if server != nil {
			go func() {
				shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				err := server.Shutdown(shutdownContext)
				if errors.Is(err, http.ErrServerClosed) {
					err = nil
				} else if err != nil {
					closeErr := server.Close()
					if errors.Is(closeErr, http.ErrServerClosed) {
						closeErr = nil
					}
					err = joinRunErrors(err, closeErr)
				}
				httpShutdown <- err
			}()
		} else {
			httpShutdown <- nil
		}
		controls.onShutdownStep(shutdownIngressQuiesced)

		if moduleCancel != nil {
			moduleCancel()
		}
		controls.onShutdownStep(shutdownModuleCanceled)
		if moduleErrors != nil {
			if err := <-moduleErrors; err != nil {
				moduleCleanupErrors = append(moduleCleanupErrors, fmt.Errorf("run Device / Entity module: %w", err))
			}
		}
		controls.onShutdownStep(shutdownModuleJoined)

		<-registrationClosed
		<-observationClosed
		if err := <-httpShutdown; err != nil {
			joinErrors = append(joinErrors, fmt.Errorf("shutdown HTTP: %w", err))
		}
		if serverErrors != nil && !serverResultConsumed {
			if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
				joinErrors = append(joinErrors, fmt.Errorf("serve HTTP: %w", err))
			}
		}
		controls.onShutdownStep(shutdownIngressJoined)

		if connection != nil {
			if err := connection.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
				natsErrors = append(natsErrors, fmt.Errorf("drain NATS connection: %w", err))
			}
			connection.Close()
		}
		controls.onShutdownStep(shutdownNATSClosed)
		if err := database.Close(); err != nil {
			databaseErrors = append(databaseErrors, fmt.Errorf("close SQLite: %w", err))
		}
		controls.onShutdownStep(shutdownDatabaseClosed)

		cleanupErrors := append(ingressErrors, moduleCleanupErrors...)
		cleanupErrors = append(cleanupErrors, joinErrors...)
		cleanupErrors = append(cleanupErrors, natsErrors...)
		cleanupErrors = append(cleanupErrors, databaseErrors...)
		return joinRunErrors(primary, cleanupErrors...)
	}

	connection, err = connectCoreNATS(ctx, config.NATSURL)
	if err != nil {
		return cleanup(startupRunError(ctx, err))
	}
	js, err := jetstream.New(connection)
	if err != nil {
		return cleanup(fmt.Errorf("create JetStream client: %w", err))
	}
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		return cleanup(startupRunError(ctx, err))
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return cleanup(fmt.Errorf("compile wire schemas: %w", err))
	}
	delivery := devicesnats.NewCommandDelivery(platformnats.NewCommandClient(connection, validator))
	moduleContext, cancelModule := context.WithCancel(context.WithoutCancel(ctx))
	moduleCancel = cancelModule
	moduleErrors = make(chan error, 1)
	go func() { moduleErrors <- service.Run(moduleContext, delivery) }()

	if err := controls.beforeRegistrationStart(); err != nil {
		return cleanup(err)
	}
	registrations, err = platformnats.StartRegistrationServer(
		connection, validator, devicesnats.RegistrationHandler(service), logger,
	)
	if err != nil {
		return cleanup(startupRunError(ctx, err))
	}
	if err := controls.beforeObservationStart(); err != nil {
		return cleanup(err)
	}
	observations, err = platformnats.StartObservationConsumer(
		ctx, durable, validator, devicesnats.ObservationHandler(service), logger,
	)
	if err != nil {
		return cleanup(startupRunError(ctx, err))
	}

	readiness := NewRuntimeReadiness(database, connection, js, observations)
	handler, _ := NewHTTPHandler(devicesapi.Dependencies{Entities: service, Commands: service}, readiness)
	server = &http.Server{Addr: config.HTTPAddr, Handler: handler}
	serverErrors = make(chan error, 1)
	go func() { serverErrors <- controls.serveHTTP(server) }()

	var primary error
	select {
	case err := <-serverErrors:
		serverResultConsumed = true
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			primary = fmt.Errorf("serve HTTP: %w", err)
		}
	case <-ctx.Done():
	}
	return cleanup(primary)
}

func startupRunError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func validateAppControls(controls appControls) error {
	switch {
	case controls.beforeRegistrationStart == nil:
		return errors.New("Registration startup control is required")
	case controls.beforeObservationStart == nil:
		return errors.New("Observation startup control is required")
	case controls.serveHTTP == nil:
		return errors.New("HTTP serve control is required")
	case controls.onShutdownStep == nil:
		return errors.New("shutdown observer is required")
	default:
		return nil
	}
}

func joinRunErrors(primary error, cleanup ...error) error {
	errorsToJoin := make([]error, 0, len(cleanup)+1)
	if primary != nil {
		errorsToJoin = append(errorsToJoin, primary)
	}
	for _, err := range cleanup {
		if err != nil {
			errorsToJoin = append(errorsToJoin, err)
		}
	}
	if len(errorsToJoin) == 0 {
		return nil
	}
	return errors.Join(errorsToJoin...)
}

func closedChannel() <-chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
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
