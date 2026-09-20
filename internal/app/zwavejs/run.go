package zwavejs

import (
	"context"
	"errors"
	"log/slog"

	zwavejsadapter "github.com/mholtzscher/hearth/internal/adapters/zwavejs"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// concurrentComponents is the number of supervised process loops: the Adapter
// runtime and the SDK command server.
const concurrentComponents = 2

// Run assembles and supervises the Z-Wave JS Adapter process. It claims one SDK
// Session with the fixed software identity, constructs the Adapter over the
// configured trusted WebSocket endpoint, and runs the Adapter beside
// Session.ServeCommands under one child context: when either loop returns,
// assembly cancels and joins its sibling. Parent cancellation is graceful, and
// assembly closes the SDK Session on return.
//
// The endpoint is a trusted-network boundary, not a security boundary. The
// embedded Z-Wave JS server has no authentication and no TLS, so v1 accepts
// only a plain ws:// loopback or trusted-private endpoint and never sends
// S0/S2 security keys, credentials, or controller-management operations.
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

// supervise runs the Adapter and the SDK command server under one child
// context. The first loop to return cancels its sibling, both are joined, and a
// terminal error from either is returned after cancellation. Context
// cancellation is graceful and reported as success.
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
