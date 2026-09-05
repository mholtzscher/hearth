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
	processLogger := logger.With("component", "process")

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

	// started flips once component supervision begins. The deferred stopping
	// record below covers the post-connect startup failure (adapter
	// construction); it is registered after the close defer so stopping always
	// precedes session release. The supervise path sets started and logs
	// stopping itself, so exactly one record is emitted on every path.
	started := false
	defer func() {
		if !started {
			processLogger.InfoContext(
				ctx, "hearth-adapter-zigbee2mqtt stopping", "event", "process.stopping",
				"reason_code", startupReason(ctx),
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

	started = true
	runErr := supervise(
		ctx,
		zigbeeAdapter.Run,
		func(runContext context.Context) error {
			return session.ServeCommands(runContext, zigbeeAdapter.HandleCommand)
		},
	)
	stoppingReason := "context_cancelled"
	if runErr != nil && ctx.Err() == nil {
		stoppingReason = "component_failure"
	}
	processLogger.InfoContext(
		ctx, "hearth-adapter-zigbee2mqtt stopping", "event", "process.stopping",
		"reason_code", stoppingReason,
	)
	return runErr
}

// startupReason distinguishes cancellation from failure for a post-connect
// startup teardown record.
func startupReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "context_cancelled"
	}
	return "startup_failed"
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
