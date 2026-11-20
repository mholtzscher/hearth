package homekit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	bridge "github.com/mholtzscher/hearth/internal/adapters/homekit"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// Run owns the adapter Session, Fact connection, and bridge runtime.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	return runSession(ctx, config, logger, runBridge)
}

func runSession(
	ctx context.Context, config Config, logger *slog.Logger,
	run func(context.Context, Config, *adapter.Session, *slog.Logger) error,
) error {
	config, err := config.Resolve()
	if err != nil {
		return err
	}
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: config.AdapterID, SoftwareName: SoftwareName, SoftwareVersion: "0.1.0",
		NATSURL: *config.NATSURL, Logger: logger,
	})
	if err != nil {
		return sessionResult(ctx, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watcher := make(chan error, 1)
	go func() {
		watcher <- sessionResult(ctx, session.Wait(ctx))
		cancel()
	}()
	err = sessionResult(ctx, run(ctx, config, session, logger))
	cancel()
	return errors.Join(err, <-watcher, session.Close())
}

func sessionResult(ctx context.Context, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

func runBridge(ctx context.Context, config Config, session *adapter.Session, logger *slog.Logger) error {
	client, err := bridge.NewCoreClient(config.CoreURL)
	if err != nil {
		return err
	}
	const connectTimeout = 2 * time.Second
	conn, err := natsgo.Connect(
		*config.NATSURL,
		natsgo.Name(SoftwareName+" Device Facts"),
		natsgo.Timeout(connectTimeout),
		natsgo.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("connect HomeKit Fact transport: %w", err)
	}
	defer conn.Close()
	js, err := jetstream.New(conn, jetstream.WithDefaultTimeout(connectTimeout))
	if err != nil {
		return fmt.Errorf("prepare HomeKit Fact reader: %w", err)
	}
	runtime, err := bridge.NewRuntime(bridge.RuntimeConfig{
		DataDir: config.DataDir, BridgeName: *config.BridgeName, DisplayUnit: *config.TemperatureDisplayUnit,
		ListenPort: *config.ListenPort, Interfaces: config.Interfaces, ResyncInterval: *config.ResyncInterval,
	}, session, client, js, logger)
	if err != nil {
		return err
	}
	return runtime.Run(ctx)
}
