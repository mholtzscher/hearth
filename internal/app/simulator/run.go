package simulator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkadapterenumeventv1 "github.com/mholtzscher/hearth/sdk/adapter/enumeventv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With(slog.String("component", "process"))
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
		// Runtime fencing is already reported by the SDK session lifecycle,
		// so a fenced close stays silent here.
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			processLogger.WarnContext(
				ctx,
				"process cleanup failed",
				slog.String("event", "process.cleanup_failed"),
				slog.String("stage", "session_close"),
				slog.String("error_code", "cleanup_failed"),
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
	entities := []adapter.EntityDescriptor{descriptor}
	// The device-events scenario adds one event-source Entity beside power. No
	// other scenario changes its Entities or Binding key, so existing
	// scenarios keep their canonical reads.
	deviceEventsScenario := config.Scenario == simulatoradapter.ScenarioDeviceEvents
	if deviceEventsScenario {
		eventDescriptor, eventDescriptorErr := sdkadapterenumeventv1.NewEntityDescriptor(adapter.EntityMetadata{
			Key: "events", ExternalID: config.BindingKey + ".events", Name: "Events",
		}, simulated.DeviceEventSupport())
		if eventDescriptorErr != nil {
			return eventDescriptorErr
		}
		entities = append(entities, eventDescriptor)
	}
	deviceExternalID := config.BindingKey
	binding, registrationErr := session.Register(ctx, adapter.Registration{
		BindingKey: config.BindingKey,
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: entities,
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
	if deviceEventsScenario {
		if err := runDeviceEventScenario(ctx, simulated, binding); err != nil {
			return err
		}
		// Registered after session.Close, so standard-input workers are joined
		// and the publisher stops before the Session closes.
		defer startDeviceEventInput(ctx, simulated, logger)()
	}
	logger.InfoContext(
		ctx,
		"simulator initialized",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.initialized"),
		slog.String("scenario", config.Scenario),
		slog.String("entity_id", entityID),
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

// runDeviceEventScenario binds the registered event-source Entity so
// EmitDeviceEvent publishes for the canonical identity Core returned.
func runDeviceEventScenario(
	ctx context.Context,
	simulated *simulatoradapter.Adapter,
	binding adapter.Binding,
) error {
	eventsEntityID, err := entityIDForKey(binding, "events")
	if err != nil {
		return err
	}
	if initializeErr := simulated.InitializeDeviceEventSource(ctx, eventsEntityID); initializeErr != nil {
		return fmt.Errorf("initialize event source Entity availability: %w", initializeErr)
	}
	return nil
}

// startDeviceEventInput starts the cancellable standard-input reader and
// returns a function that stops it and joins its workers. Callers invoke the
// returned function before the Session closes.
func startDeviceEventInput(
	ctx context.Context,
	simulated *simulatoradapter.Adapter,
	logger *slog.Logger,
) func() {
	inputContext, stopInput := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Go(func() {
		RunDeviceEventInput(inputContext, os.Stdin, simulated, logger)
	})
	return func() {
		stopInput()
		workers.Wait()
	}
}
