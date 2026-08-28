package homeassistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	homeassistantadapter "github.com/mholtzscher/hearth/internal/adapters/homeassistant"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

const (
	registrationRetryMinimum = 100 * time.Millisecond
	registrationRetryMaximum = 2 * time.Second
	powerEntityKey           = "power"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	tokenBytes, err := os.ReadFile(config.Upstream.TokenFile)
	if err != nil {
		return fmt.Errorf("read Home Assistant token file: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return errors.New("Home Assistant token file is empty")
	}

	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: config.AdapterID, NATSURL: config.NATSURL})
	if err != nil {
		return err
	}
	defer session.Close()

	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key:        powerEntityKey,
		ExternalID: config.Binding.EntityExternalID,
		Name:       config.Binding.EntityName,
	}, homeassistantadapter.PowerSupport())
	if err != nil {
		return err
	}
	binding, err := register(ctx, session, adapter.Registration{
		BindingKey: config.Binding.Key,
		Device: adapter.DeviceDescriptor{
			ExternalID: config.Binding.DeviceExternalID,
			Name:       config.Binding.DeviceName,
			Kind:       "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	}, logger)
	if err != nil {
		return err
	}
	entityID, err := entityIDForKey(binding, powerEntityKey)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "registered Home Assistant light", "device_id", binding.DeviceID, "entity_id", entityID)

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
	results := make(chan error, 2)
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
		wait := registrationJitter(delay)
		logger.WarnContext(ctx, "retry Home Assistant adapter registration", "error", err, "retry_in", wait)
		timer := time.NewTimer(wait)
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

func registrationJitter(delay time.Duration) time.Duration {
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
