package simulator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With("component", "process")
	// The SDK owns session logging and attaches its own adapter_session
	// component and Adapter/runtime identity, so it receives the root logger.
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
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			processLogger.WarnContext(
				ctx,
				"process cleanup failed",
				"event",
				"process.cleanup_failed",
				"stage",
				"session_close",
				"error_code",
				"cleanup_failed",
			)
		}
	}()
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
	binding, registrationErr := session.Register(ctx, adapter.Registration{
		BindingKey: config.BindingKey,
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	})
	if registrationErr != nil {
		return registrationErr
	}
	entityID, entityIDErr := entityIDForKey(binding, "power")
	if entityIDErr != nil {
		return entityIDErr
	}
	if err := simulated.Initialize(ctx, entityID); err != nil {
		return fmt.Errorf("initialize simulator health and Entity availability: %w", err)
	}
	logger.With("component", "simulator").InfoContext(
		ctx,
		"simulator initialized",
		"event",
		"simulator.initialized",
		"scenario",
		config.Scenario,
		"entity_id",
		entityID,
	)
	handler, err := simulated.CommandHandler(entityID)
	if err != nil {
		return err
	}
	serveErr := session.ServeCommands(ctx, handler)
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) &&
		!errors.Is(serveErr, adapter.ErrClosed) {
		return serveErr
	}
	return nil
}

func entityIDForKey(binding adapter.Binding, key string) (string, error) {
	for _, entity := range binding.Entities {
		if entity.Key == key {
			return entity.EntityID, nil
		}
	}
	return "", fmt.Errorf("registration response omitted Entity key %q", key)
}
