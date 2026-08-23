package hearthd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	receiptPruneInterval = time.Hour
	shutdownTimeout      = 5 * time.Second
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		return fmt.Errorf("construct entity type catalog: %w", err)
	}
	database, err := platformdb.Open(ctx, config.SQLitePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := platformdb.Migrate(ctx, database); err != nil {
		return err
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(repository, catalog, devices.Dependencies{})
	startupTime := time.Now().UTC()
	if err := repository.InterruptActiveCommands(ctx, startupTime); err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	if err := service.DeleteExpiredObservationReceipts(ctx, startupTime); err != nil {
		return fmt.Errorf("prune observation receipts: %w", err)
	}

	connection, err := connectCoreNATS(ctx, config.NATSURL)
	if err != nil {
		return err
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return fmt.Errorf("create JetStream client: %w", err)
	}
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		return err
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return fmt.Errorf("compile wire schemas: %w", err)
	}

	registrations, err := platformnats.StartRegistrationServer(connection, validator, registrationHandler(service), logger)
	if err != nil {
		return err
	}
	defer registrations.Drain()
	observations, err := platformnats.StartObservationConsumer(ctx, durable, validator, observationHandler(service), logger)
	if err != nil {
		return err
	}
	defer observations.Stop()

	readiness := NewRuntimeReadiness(database, connection, js, observations)
	handler, _ := NewHTTPHandler(service, readiness)
	server := &http.Server{Addr: config.HTTPAddr, Handler: handler}
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
		if err := registrations.Drain(); err != nil {
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

func registrationHandler(service *devices.Service) platformnats.RegistrationHandler {
	return func(ctx context.Context, adapterID string, registration platformnats.Registration) (platformnats.RegistrationResponse, error) {
		domainRegistration := devices.Registration{
			BindingKey: registration.BindingKey,
			Device: devices.DeviceDescriptor{
				ExternalID: copyStringPointer(registration.Device.ExternalID),
				Name:       registration.Device.Name,
				Kind:       devices.DeviceKind(registration.Device.Kind),
			},
			Entities: make([]devices.EntityDescriptor, len(registration.Entities)),
		}
		for index, entity := range registration.Entities {
			domainRegistration.Entities[index] = devices.EntityDescriptor{
				Key: entity.Key, ExternalID: entity.ExternalID, Name: entity.Name,
				TypeID: devices.EntityTypeID(entity.Type), Support: devices.EntitySupport(append(json.RawMessage(nil), entity.Support...)),
			}
		}
		binding, err := service.Register(ctx, adapterID, domainRegistration)
		var rejected *devices.RegistrationRejectedError
		if errors.As(err, &rejected) {
			return platformnats.RegistrationResponse{
				Status: "rejected",
				Error:  &platformnats.RegistrationError{Code: string(rejected.Code), Message: rejected.Message},
			}, nil
		}
		if err != nil {
			return platformnats.RegistrationResponse{}, err
		}
		wireBinding := platformnats.Binding{
			BindingKey: binding.BindingKey, DeviceID: string(binding.DeviceID),
			Entities: make([]platformnats.EntityBinding, len(binding.Entities)),
		}
		for index, entity := range binding.Entities {
			wireBinding.Entities[index] = platformnats.EntityBinding{Key: entity.Key, EntityID: string(entity.EntityID)}
		}
		return platformnats.RegistrationResponse{Status: "accepted", Binding: &wireBinding}, nil
	}
}

func observationHandler(service *devices.Service) platformnats.ObservationHandler {
	return func(ctx context.Context, delivery platformnats.ObservationDelivery) error {
		observation, err := domainObservation(delivery.Envelope)
		if err != nil {
			return err
		}
		_, err = service.ProjectObservation(ctx, delivery.Route.AdapterID, observation, delivery.ObservedAt)
		return err
	}
}

func domainObservation(envelope platformnats.Envelope[platformnats.Observation]) (devices.Observation, error) {
	observationID, err := devices.ParseObservationID(envelope.ID)
	if err != nil {
		return devices.Observation{}, err
	}
	entityID, err := devices.ParseEntityID(envelope.Data.EntityID)
	if err != nil {
		return devices.Observation{}, err
	}
	adapterReceivedAt, err := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if err != nil {
		return devices.Observation{}, err
	}
	observation := devices.Observation{
		ID: observationID, EntityID: entityID, Value: devices.Value(append(json.RawMessage(nil), envelope.Data.Value...)),
		AdapterReceivedAt: adapterReceivedAt,
	}
	if envelope.Data.SourceUpdatedAt != nil {
		sourceUpdatedAt, err := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if err != nil {
			return devices.Observation{}, err
		}
		observation.SourceUpdatedAt = &sourceUpdatedAt
	}
	if envelope.Data.RefreshForCommand != nil {
		commandID, err := devices.ParseCommandID(*envelope.Data.RefreshForCommand)
		if err != nil {
			return devices.Observation{}, err
		}
		observation.RefreshForCommand = &commandID
	}
	return observation, nil
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
				logger.Error("prune observation receipts", "error", err)
			}
		}
	}
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
