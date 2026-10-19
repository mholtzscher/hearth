package discord

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	discordadapter "github.com/mholtzscher/hearth/internal/adapters/discord"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	return run(ctx, config, logger, nil)
}

func run(ctx context.Context, config Config, logger *slog.Logger, transport http.RoundTripper) (result error) {
	defer func() {
		if ctx.Err() != nil && (errors.Is(result, context.Canceled) || errors.Is(result, context.DeadlineExceeded)) {
			result = nil
		}
	}()
	if err := config.Validate(); err != nil {
		return err
	}
	routes, err := loadCredentials(config)
	if err != nil {
		return err
	}
	client, err := discordadapter.New(routes, transport)
	if err != nil {
		return err
	}
	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: config.AdapterID,
		SoftwareName: "hearth-adapter-discord", SoftwareVersion: "0.1.0", NATSURL: config.NATSURL, Logger: logger})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	server, err := session.StartNotificationServer(
		ctx,
		func(sendCtx context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
			return client.Send(sendCtx, request.LocalKey, request.Message)
		},
	)
	if err != nil {
		return err
	}
	// The standard HTTP transport obeys cancellation and the absolute send deadline.
	// Join actual returns before Session.Close releases runtime ownership.
	defer func() { _ = server.Close(context.WithoutCancel(ctx)) }()
	functionServer, err := session.StartFunctionServer(ctx, client.Functions())
	if err != nil {
		return err
	}
	defer func() { _ = functionServer.Close(context.WithoutCancel(ctx)) }()
	keys := make([]string, 0, len(config.Destinations))
	for key := range config.Destinations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err = session.RegisterNotificationDestination(ctx, key, config.Destinations[key].Name); err != nil {
			return err
		}
	}
	if err = session.SetHealth(
		ctx,
		adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC()},
	); err != nil {
		return err
	}
	return session.Wait(ctx)
}
