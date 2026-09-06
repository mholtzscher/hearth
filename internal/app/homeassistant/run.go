package homeassistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	homeassistantadapter "github.com/mholtzscher/hearth/internal/adapters/homeassistant"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	powerEntityKey       = "power"
	concurrentComponents = 2
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With("component", "process")
	token, err := readTokenFile(config.Upstream.TokenFile)
	if err != nil {
		return err
	}

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID:       config.AdapterID,
		SoftwareName:    "hearth-adapter-homeassistant",
		SoftwareVersion: "0.1.0",
		NATSURL:         config.NATSURL,
		Logger:          logger,
	})
	if err != nil {
		return err
	}
	defer closeAdapterSession(ctx, session, processLogger)
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key:        powerEntityKey,
		ExternalID: config.Binding.EntityExternalID,
		Name:       config.Binding.EntityName,
	}, homeassistantadapter.PowerSupport())
	if err != nil {
		return err
	}
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: config.Binding.Key,
		Device: adapter.DeviceDescriptor{
			ExternalID: config.Binding.DeviceExternalID,
			Name:       config.Binding.DeviceName,
			Kind:       "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		return err
	}
	entityID, err := entityIDForKey(binding, powerEntityKey)
	if err != nil {
		return err
	}
	// Registration outcome logging belongs to the SDK session claim owner;
	// no duplicate registration summary is emitted here.

	migrationAdapter, err := homeassistantadapter.New(session, homeassistantadapter.Config{
		URL:              config.Upstream.URL,
		Token:            token,
		ExternalEntityID: config.Binding.EntityExternalID,
		EntityID:         entityID,
	}, logger)
	if err != nil {
		return err
	}
	handler, err := migrationAdapter.CommandHandler()
	if err != nil {
		return err
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, concurrentComponents)
	go func() { results <- migrationAdapter.Run(runContext) }()
	go func() { results <- session.ServeCommands(runContext, handler) }()

	first := <-results
	cancel()
	second := <-results
	if ctx.Err() != nil {
		return nil
	}
	for _, runErr := range []error{first, second} {
		if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, adapter.ErrClosed) {
			return runErr
		}
	}
	return nil
}

// readTokenFile loads the upstream bearer token without echoing its value.
func readTokenFile(path string) (string, error) {
	tokenBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Home Assistant token file: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return "", errors.New("home assistant token file is empty")
	}
	return token, nil
}

// closeAdapterSession releases the SDK session, warning on cleanup failure
// without changing the caller's return semantics.
func closeAdapterSession(ctx context.Context, session *adapter.Session, logger *slog.Logger) {
	if closeErr := session.Close(); closeErr != nil {
		logger.WarnContext(
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
}

func entityIDForKey(binding adapter.Binding, key string) (string, error) {
	for _, entity := range binding.Entities {
		if entity.Key == key {
			return entity.EntityID, nil
		}
	}
	return "", fmt.Errorf("registration response omitted Entity key %q", key)
}
