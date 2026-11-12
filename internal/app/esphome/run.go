package esphome

import (
	"context"
	"errors"
	"log/slog"

	esphomeadapter "github.com/mholtzscher/hearth/internal/adapters/esphome"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// concurrentComponents counts the Adapter runtime and SDK command server loops.
const concurrentComponents = 2

// Run supervises the Adapter and SDK Session under a shared child context. When either loop returns, it cancels and
// joins the other. Parent cancellation is graceful, and Run closes the Session on return.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	prepared, err := adapterConfig(config)
	if err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	processLogger := logger.With(slog.String("component", "process"))

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID:       config.AdapterID,
		SoftwareName:    "hearth-adapter-esphome",
		SoftwareVersion: "0.1.0",
		NATSURL:         config.NATSURL,
		Logger:          logger,
	})
	if err != nil {
		return err
	}
	defer func() {
		// The SDK reports runtime fencing, so a fenced close stays silent.
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

	deviceAdapter, err := esphomeadapter.New(
		session,
		prepared,
		logger,
	)
	if err != nil {
		return err
	}

	return supervise(
		ctx,
		deviceAdapter.Run,
		func(runContext context.Context) error {
			return session.ServeCommands(runContext, deviceAdapter.HandleCommand)
		},
	)
}

// supervise cancels the sibling when either loop returns and joins both. It returns terminal errors, but treats parent
// cancellation as success.
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
