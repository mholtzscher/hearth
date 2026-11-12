package levoit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	purifier "github.com/mholtzscher/hearth/internal/adapters/levoit"
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
	credentials, err := readCredentials(config.CredentialsFile)
	if err != nil {
		return err
	}
	runtimeConfig, err := config.runtimeConfig()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: config.AdapterID,
		SoftwareName: "hearth-adapter-levoit", SoftwareVersion: "0.1.0", NATSURL: config.NATSURL, Logger: logger})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			logger.WarnContext(ctx, "process cleanup failed", slog.String("event", "process.cleanup_failed"),
				slog.String("stage", "session_close"), slog.String("error_code", "cleanup_failed"))
		}
	}()
	instance, err := purifier.New(session, runtimeConfig, credentials, transport, logger)
	if err != nil {
		return err
	}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	polling := lifecycle.StartWorker(lifetime, instance.Run)
	commands := lifecycle.StartWorker(lifetime, func(ctx context.Context) error {
		return session.ServeCommands(ctx, instance.HandleCommand)
	})
	watcher := lifecycle.StartWorker(lifetime, session.Wait)
	select {
	case <-ctx.Done():
	case <-polling.Closed():
	case <-commands.Closed():
	case <-watcher.Closed():
	}
	cancel()
	join := context.WithoutCancel(ctx)
	// Unlike the SDK's subscription drain, this joins actual Adapter handlers.
	if err = instance.CloseCommands(join); err != nil {
		return err
	}
	var result error
	for _, worker := range []*lifecycle.WorkerHandle{polling, commands, watcher} {
		if err = worker.Wait(join); err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, err)
		}
	}
	return result
}
