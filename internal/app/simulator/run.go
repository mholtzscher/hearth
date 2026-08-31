package simulator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	registrationRetryMinimum = 100 * time.Millisecond
	registrationRetryMaximum = 2 * time.Second
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	session, connectErr := adapter.Connect(ctx, adapter.Config{
		AdapterID:       config.AdapterID,
		SoftwareName:    "hearth-simulator",
		SoftwareVersion: "0.1.0",
		NATSURL:         config.NATSURL,
		Logger:          logger,
	})
	if connectErr != nil {
		return connectErr
	}
	defer session.Close()
	simulated, simulatorErr := simulatoradapter.New(session, config.Scenario)
	if simulatorErr != nil {
		return simulatorErr
	}
	descriptor, descriptorErr := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: config.BindingKey + ".power", Name: "Power",
	}, simulated.Support())
	if descriptorErr != nil {
		return descriptorErr
	}
	deviceExternalID := config.BindingKey
	binding, registrationErr := register(ctx, session, adapter.Registration{
		BindingKey: config.BindingKey,
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	}, logger)
	if registrationErr != nil {
		return registrationErr
	}
	entityID, entityIDErr := entityIDForKey(binding, "power")
	if entityIDErr != nil {
		return entityIDErr
	}
	if err := simulated.PublishInitial(ctx, entityID); err != nil {
		return fmt.Errorf("publish initial simulator Observation: %w", err)
	}
	if config.Scenario == simulatoradapter.ScenarioUnavailableAdapter {
		<-ctx.Done()
		return nil
	}
	handler, err := simulated.CommandHandler(entityID)
	if err != nil {
		return err
	}
	if serveErr := session.ServeCommands(
		ctx,
		handler,
	); serveErr != nil && !errors.Is(serveErr, context.Canceled) &&
		!errors.Is(serveErr, adapter.ErrClosed) {
		return serveErr
	}
	return nil
}

func register(
	ctx context.Context,
	session *adapter.Session,
	registration adapter.Registration,
	logger *slog.Logger,
) (adapter.Binding, error) {
	delay := registrationRetryMinimum
	for {
		binding, err := session.Register(ctx, registration)
		if err == nil {
			return binding, nil
		}
		var validation *adapter.ValidationError
		var rejected *adapter.RegistrationRejectedError
		if errors.As(err, &validation) || errors.As(err, &rejected) {
			return adapter.Binding{}, err
		}
		logger.WarnContext(ctx, "retry simulator registration", "error", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return adapter.Binding{}, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > registrationRetryMaximum {
			delay = registrationRetryMaximum
		}
	}
}

func entityIDForKey(binding adapter.Binding, key string) (string, error) {
	for _, entity := range binding.Entities {
		if entity.Key == key {
			return entity.EntityID, nil
		}
	}
	return "", fmt.Errorf("registration response omitted Entity key %q", key)
}
