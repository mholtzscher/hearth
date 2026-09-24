package simulator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// runMultipleScriptedAdapters keeps each Adapter's Session and health independent.
func runMultipleScriptedAdapters(ctx context.Context, config Config, logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	type started struct {
		id      string
		runtime *scripted.Runtime
	}
	ready := make(chan started, len(config.Adapters))
	failures := make(chan error, len(config.Adapters)+1)
	var sessions sync.WaitGroup
	defer func() {
		cancel()
		sessions.Wait()
	}()
	for _, entry := range config.Adapters {
		sessions.Go(func() {
			session, err := adapter.Connect(ctx, adapter.Config{
				AdapterID: entry.AdapterID, SoftwareName: "hearth-simulator",
				SoftwareVersion: "0.1.0", NATSURL: config.NATSURL, Logger: logger,
			})
			if err == nil {
				err = runScriptedSessionReady(
					ctx, Config{AdapterID: entry.AdapterID, Devices: entry.Devices}, session, logger,
					func(runtime *scripted.Runtime) { ready <- started{entry.AdapterID, runtime} },
				)
			}
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				err = fmt.Errorf("session stopped unexpectedly")
			}
			failures <- fmt.Errorf("adapter %q: %w", entry.AdapterID, err)
		})
	}
	runtimes := make(map[string]*scripted.Runtime, len(config.Adapters))
	for len(runtimes) < len(config.Adapters) {
		select {
		case result := <-ready:
			runtimes[result.id] = result.runtime
		case err := <-failures:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if config.ControlAddr != "" {
		go func() { failures <- ServeAdapterControl(ctx, config.ControlAddr, runtimes, logger) }()
	}
	select {
	case err := <-failures:
		return err
	case <-ctx.Done():
		return nil
	}
}
