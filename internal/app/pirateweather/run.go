package pirateweather

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	weather "github.com/mholtzscher/hearth/internal/adapters/pirateweather"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	return run(ctx, config, logger, nil)
}

func run(ctx context.Context, config Config, logger *slog.Logger, transport http.RoundTripper) error {
	if err := config.Validate(); err != nil {
		return err
	}
	key, err := readSecret(config.APIKeyFile)
	if err != nil {
		return err
	}
	runtimeConfig, err := config.runtimeConfig()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if logger == nil {
		logger = slog.Default()
	}
	process := logger.With(slog.String("component", "process"))
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID:       config.AdapterID,
		SoftwareName:    "hearth-adapter-pirateweather",
		SoftwareVersion: "0.1.0",
		NATSURL:         config.NATSURL,
		Logger:          logger,
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			process.WarnContext(ctx, "process cleanup failed", slog.String("event", "process.cleanup_failed"),
				slog.String("stage", "session_close"), slog.String("error_code", "cleanup_failed"))
		}
	}()
	instance, err := weather.New(session, runtimeConfig, key, transport, logger)
	if err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	server, err := session.StartFunctionServer(runContext, functionsWithLifetime(runContext, instance.Functions()))
	if err != nil {
		return err
	}
	polling := lifecycle.StartWorker(runContext, instance.Run)
	watcher := lifecycle.StartWorker(runContext, session.Wait)
	select {
	case <-ctx.Done():
	case <-polling.Closed():
	case <-watcher.Closed():
	}
	cancel()
	// HTTP is cancelable. Join actual handlers and workers before releasing NATS.
	joinContext := context.WithoutCancel(ctx)
	closeErr := server.Close(joinContext)
	pollErr := polling.Wait(joinContext)
	watchErr := watcher.Wait(joinContext)
	if closeErr != nil {
		process.WarnContext(joinContext, "process cleanup failed", slog.String("event", "process.cleanup_failed"),
			slog.String("stage", "functions_close"), slog.String("error_code", "cleanup_failed"))
	}
	for _, result := range []error{pollErr, watchErr} {
		if result != nil && !errors.Is(result, context.Canceled) {
			return result
		}
	}
	return nil
}

// The SDK deliberately detaches Function admission from its registration context
// to permit draining. These read-only HTTP requests instead stop with this process.
func functionsWithLifetime(lifetime context.Context, functions []adapter.Function) []adapter.Function {
	for index := range functions {
		handler := functions[index].Handler
		functions[index].Handler = func(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(lifetime, cancel)
			defer stop()
			return handler(ctx, request)
		}
	}
	return functions
}
