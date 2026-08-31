package hearthd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	receiptPruneInterval  = time.Hour
	shutdownTimeout       = 5 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	natsReconnectWait     = 250 * time.Millisecond
)

//nolint:gocognit // Startup and shutdown remain linear so resource ownership is visible in one place.
func Run(ctx context.Context, config Config, logger *slog.Logger) error { //nolint:funlen // Linear resource lifecycle.
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	catalog, catalogErr := devices.NewBuiltinTypeCatalog()
	if catalogErr != nil {
		return fmt.Errorf("construct entity type catalog: %w", catalogErr)
	}
	database, openErr := platformdb.Open(ctx, config.SQLitePath)
	if openErr != nil {
		return openErr
	}
	defer database.Close()
	if err := platformdb.Migrate(ctx, database); err != nil {
		return err
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	startupTime := time.Now().UTC()
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	if err := repository.DeleteExpiredObservationReceipts(ctx, startupTime); err != nil {
		return fmt.Errorf("prune observation receipts: %w", err)
	}

	connection, connectErr := connectCoreNATS(ctx, config.NATSURL)
	if connectErr != nil {
		return connectErr
	}
	defer connection.Close()
	js, jetStreamErr := jetstream.New(connection)
	if jetStreamErr != nil {
		return fmt.Errorf("create JetStream client: %w", jetStreamErr)
	}
	durable, provisionErr := devicesnats.ProvisionObservationResources(ctx, js)
	if provisionErr != nil {
		return provisionErr
	}
	validator, compileErr := contractsv1.Compile()
	if compileErr != nil {
		return fmt.Errorf("compile wire schemas: %w", compileErr)
	}
	commandSender := devicesnats.NewCommandSender(connection, validator)
	service := devices.NewService(repository, commandSender, catalog, devices.Dependencies{})

	sessions, sessionErr := devicesnats.StartSessionServer(connection, validator, service, service, logger)
	if sessionErr != nil {
		return sessionErr
	}
	defer func() { _ = sessions.Drain() }()
	availability, availabilityErr := devicesnats.StartEntityAvailabilityServer(
		connection, validator, service, logger,
	)
	if availabilityErr != nil {
		return availabilityErr
	}
	defer func() { _ = availability.Drain() }()
	registrations, registrationErr := devicesnats.StartRegistrationServer(connection, validator, service, logger)
	if registrationErr != nil {
		return registrationErr
	}
	defer func() { _ = registrations.Drain() }()
	enablement, enablementErr := devicesnats.StartEntityEnablementServer(connection, validator, service, logger)
	if enablementErr != nil {
		return enablementErr
	}
	defer func() { _ = enablement.Drain() }()
	observations, observationErr := devicesnats.StartObservationConsumer(ctx, durable, validator, service, logger)
	if observationErr != nil {
		return observationErr
	}
	defer observations.Stop()

	readiness := NewRuntimeReadiness(database, connection, js, observations)
	healthSupervisor := startHealthSupervisor(ctx, readiness, service, logger)
	defer healthSupervisor.Stop()
	handler, _ := NewHTTPHandler(service, readiness)
	server := &http.Server{
		Addr: config.HTTPAddr, Handler: handler, ReadHeaderTimeout: httpReadHeaderTimeout,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	go pruneObservationReceipts(ctx, service, logger)

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		healthSupervisor.Stop()
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		observations.Drain()
		select {
		case <-observations.Closed():
		case <-shutdownContext.Done():
			observations.Stop()
		}
		if err := enablement.Drain(); err != nil {
			return err
		}
		if err := registrations.Drain(); err != nil {
			return err
		}
		if err := availability.Drain(); err != nil {
			return err
		}
		if err := sessions.Drain(); err != nil {
			return err
		}
		if err := connection.Drain(); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
			return fmt.Errorf("drain NATS connection: %w", err)
		}
		return nil
	}
}

func connectCoreNATS(ctx context.Context, url string) (*natsgo.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := []natsgo.Option{
		natsgo.Name("hearthd"),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(natsReconnectWait),
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

func pruneObservationReceipts(ctx context.Context, service *devices.Service, logger *slog.Logger) {
	ticker := time.NewTicker(receiptPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := service.DeleteExpiredObservationReceipts(ctx, now.UTC()); err != nil {
				logger.ErrorContext(ctx, "prune observation receipts", "error", err)
			}
		}
	}
}
