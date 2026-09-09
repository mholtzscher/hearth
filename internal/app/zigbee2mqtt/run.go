package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"

	zigbee2mqttadapter "github.com/mholtzscher/hearth/internal/adapters/zigbee2mqtt"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const concurrentComponents = 2

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With(slog.String("component", "process"))

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID:       config.AdapterID,
		SoftwareName:    "hearth-adapter-zigbee2mqtt",
		SoftwareVersion: "0.1.0",
		NATSURL:         config.NATSURL,
		Logger:          logger,
	})
	if err != nil {
		return err
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

	zigbeeAdapter, err := zigbee2mqttadapter.New(session, zigbee2mqttadapter.Config{
		MQTTURL:   config.MQTT.URL,
		BaseTopic: config.MQTT.BaseTopic,
		ClientID:  DeriveClientID(config.AdapterID),
	}, logger)
	if err != nil {
		return err
	}

	return supervise(
		ctx,
		zigbeeAdapter.Run,
		func(runContext context.Context) error {
			return session.ServeCommands(runContext, zigbeeAdapter.HandleCommand)
		},
	)
}

func supervise(
	ctx context.Context,
	runAdapter func(context.Context) error,
	serveCommands func(context.Context) error,
) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, concurrentComponents)
	go func() { results <- runAdapter(runContext) }()
	go func() { results <- serveCommands(runContext) }()

	first := <-results
	cancel()
	second := <-results
	for _, runErr := range []error{first, second} {
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			return runErr
		}
	}
	return nil
}
