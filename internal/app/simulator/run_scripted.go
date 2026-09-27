package simulator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// scriptedSession is the Session surface runScriptedSession drives: the
// scripted runtime's publication seam plus the SDK calls the process owns.
// *adapter.Session satisfies it, and tests substitute a scripted fake.
type scriptedSession interface {
	scripted.Session
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	ServeCommands(context.Context, adapter.CommandHandler) error
	Close() error
}

func runScripted(ctx context.Context, config Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	return runMultipleScriptedAdapters(ctx, config, logger)
}

// runScriptedSession owns one connected Session: registration, scripted
// startup, Command serving, and shutdown ordering. logger must be non-nil.
func runScriptedSession(
	ctx context.Context,
	entry ScriptedAdapterConfig,
	session scriptedSession,
	logger *slog.Logger,
	controlAddr string,
) error {
	return runScriptedSessionReady(ctx, entry, session, logger, controlAddr, nil)
}

func runScriptedSessionReady(
	ctx context.Context,
	entry ScriptedAdapterConfig,
	session scriptedSession,
	logger *slog.Logger,
	controlAddr string,
	ready func(*scripted.Runtime),
) error {
	processLogger := logger.With(slog.String("component", "process"))
	defer func() {
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
	runtime, runtimeErr := scripted.New(session, entry.Devices)
	if runtimeErr != nil {
		return runtimeErr
	}
	bindings := make([]adapter.Binding, 0, len(entry.Devices))
	for _, registration := range runtime.Registrations() {
		binding, registrationErr := session.Register(ctx, registration)
		if registrationErr != nil {
			return registrationErr
		}
		bindings = append(bindings, binding)
	}
	if attachErr := runtime.Attach(bindings); attachErr != nil {
		return attachErr
	}
	if initializeErr := runtime.Initialize(ctx); initializeErr != nil {
		return fmt.Errorf("initialize scripted health, availability, and first values: %w", initializeErr)
	}
	if ready != nil {
		ready(runtime)
	}
	// Scripts, Entity tickers, and the optional control channel all run on one
	// worker context so they can be stopped independently of the caller's
	// context. A Session failure returns from ServeCommands while the caller's
	// context is still live, so joining the workers on that context would block
	// shutdown forever.
	workerCtx, stopWorkers := context.WithCancel(ctx)
	// Command serving runs on its own child context so a failed control listener
	// can stop serving without cancelling the caller's context, which outlives
	// this session.
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	joinScripts := runtime.StartScripts(workerCtx, logger)
	// controlFailures carries the control listener's failure. It is buffered so
	// the control worker never blocks when the Session fails first and the
	// failure goes unread.
	controlFailures := make(chan error, 1)
	var controlDone sync.WaitGroup
	if controlAddr != "" {
		controlDone.Go(func() {
			controlErr := ServeControl(workerCtx, controlAddr, runtime, logger)
			if controlErr == nil || workerCtx.Err() != nil {
				// A cancelled worker context is a listener stopped by shutdown,
				// not a failed control channel.
				return
			}
			processLogger.ErrorContext(ctx, "simulator control channel failed",
				slog.String("event", "simulator.control_failed"),
				slog.String("error_code", "control_failed"),
			)
			controlFailures <- controlErr
			stopWorkers()
			stopServing()
		})
	}
	// Registered after the Session-close defer above, so it runs first and
	// publishers and the control listener stop before the Session closes.
	defer func() {
		stopWorkers()
		joinScripts()
		controlDone.Wait()
	}()
	logger.InfoContext(
		ctx,
		"simulator initialized",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.initialized"),
		slog.String("mode", "scripted"),
		slog.Int("devices", len(entry.Devices)),
		slog.Int("entities", len(runtime.Snapshot())),
	)
	serveErr := session.ServeCommands(serveCtx, runtime.CommandHandler())
	// A failed control listener cancels serveCtx, which surfaces in serveErr as
	// context.Canceled. Report the listener failure instead of returning nil, so
	// a process without its requested control API never looks initialized.
	select {
	case controlErr := <-controlFailures:
		return fmt.Errorf("control channel %s failed: %w", controlAddr, controlErr)
	default:
	}
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) &&
		!errors.Is(serveErr, adapter.ErrClosed) {
		return serveErr
	}
	return nil
}
