package zwavejs

import (
	"context"
	"errors"
	"log/slog"

	zwavejsadapter "github.com/mholtzscher/hearth/internal/adapters/zwavejs"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// concurrentComponents counts the Adapter runtime and SDK command server loops.
const concurrentComponents = 2

// Run supervises the Adapter and SDK Session under a shared child context. When either loop returns, it cancels and
// joins the other. Parent cancellation is graceful, and Run closes the Session on return.
//
// The endpoint is a trusted-network boundary, not a security boundary. The embedded Z-Wave JS server has no
// authentication or TLS. v1 accepts only a plain ws:// loopback or trusted-private endpoint and never sends S0/S2
// security keys, credentials, or controller-management operations.
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
		SoftwareName:    "hearth-adapter-zwavejs",
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

	zwaveAdapter, err := zwavejsadapter.New(
		session,
		zwavejsadapter.Config{URL: config.ZWaveJS.URL},
		logger,
	)
	if err != nil {
		return err
	}

	return supervise(
		ctx,
		zwaveAdapter.Run,
		func(runContext context.Context) error {
			return session.ServeCommands(runContext, zwaveAdapter.HandleCommand)
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
