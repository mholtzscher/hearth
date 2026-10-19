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
	StartFunctionServer(context.Context, []adapter.Function) (*adapter.FunctionServer, error)
	StartNotificationServer(context.Context, adapter.NotificationHandler) (*adapter.NotificationServer, error)
	RegisterNotificationDestination(context.Context, string, string) (string, error)
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

//nolint:gocognit,gocyclo,cyclop,funlen // Startup, serving supervision and ordered joins form one Session lifecycle.
func runScriptedSessionReady(
	ctx context.Context,
	entry ScriptedAdapterConfig,
	session scriptedSession,
	logger *slog.Logger,
	controlAddr string,
	ready func(*scripted.Runtime, *scripted.NotificationRuntime, *scripted.FunctionRuntime),
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
	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	notifications, err := scripted.NewNotificationRuntime(entry.AdapterID, entry.Notifications)
	if err != nil {
		return err
	}
	failures := make(chan error, 1)
	reportFailure := func(err error) {
		select {
		case failures <- err:
		default:
		}
		stopWorkers()
	}
	var workers sync.WaitGroup
	var notificationServer *adapter.NotificationServer
	var functionServer *adapter.FunctionServer
	var functions *scripted.FunctionRuntime
	defer func() {
		stopWorkers()
		// The SDK derives handler contexts without this worker's cancellation,
		// so unblock held handlers before Close joins them.
		if functions != nil {
			functions.Stop()
		}
		if functionServer != nil {
			_ = functionServer.Close(context.WithoutCancel(ctx))
		}
		if notificationServer != nil {
			_ = notificationServer.Close(context.WithoutCancel(ctx))
		}
		workers.Wait()
	}()
	if len(entry.Notifications) > 0 {
		notificationServer, err = session.StartNotificationServer(workerCtx, notifications.Handler())
		if err != nil {
			return err
		}
		workers.Go(func() {
			waitErr := notificationServer.Wait(workerCtx)
			if workerCtx.Err() == nil {
				reportFailure(fmt.Errorf("notification server stopped: %w", waitErr))
			}
		})
		for _, spec := range notifications.Registrations() {
			id, registrationErr := session.RegisterNotificationDestination(workerCtx, spec.Key, spec.Name)
			if registrationErr != nil {
				select {
				case failure := <-failures:
					return failure
				default:
					return registrationErr
				}
			}
			if err = notifications.Attach(spec.Key, id); err != nil {
				return err
			}
		}
	}
	functions, err = scripted.NewFunctionRuntime(entry.Functions)
	if err != nil {
		return err
	}
	if len(entry.Functions) > 0 {
		// Handlers may run before StartFunctionServer returns, and the SDK joins
		// them after a cancelled registration. Stop holds on worker cancellation
		// so an ambiguous registration cannot wait out a handler deadline.
		stopHolds := context.AfterFunc(workerCtx, functions.Stop)
		functionServer, err = session.StartFunctionServer(workerCtx, functions.Functions())
		stopHolds()
		if err != nil {
			return err
		}
		workers.Go(func() {
			waitErr := functionServer.Wait(workerCtx)
			if workerCtx.Err() == nil {
				reportFailure(fmt.Errorf("function server stopped: %w", waitErr))
			}
		})
	}
	runtime, runtimeErr := scripted.New(session, entry.Devices)
	if runtimeErr != nil {
		return runtimeErr
	}
	bindings := make([]adapter.Binding, 0, len(entry.Devices))
	for _, registration := range runtime.Registrations() {
		binding, registrationErr := session.Register(workerCtx, registration)
		if registrationErr != nil {
			return registrationErr
		}
		bindings = append(bindings, binding)
	}
	if attachErr := runtime.Attach(bindings); attachErr != nil {
		return attachErr
	}
	if initializeErr := runtime.Initialize(workerCtx); initializeErr != nil {
		return fmt.Errorf("initialize scripted health, availability, and first values: %w", initializeErr)
	}
	if ready != nil {
		if workerCtx.Err() != nil {
			return workerCtx.Err()
		}
		ready(runtime, notifications, functions)
	}
	// Scripts, Entity tickers, and the optional control channel all run on one
	// worker context so they can be stopped independently of the caller's
	// context. A Session failure returns from ServeCommands while the caller's
	// context is still live, so joining the workers on that context would block
	// shutdown forever.
	// Command serving runs on its own child context so a failed control listener
	// can stop serving without cancelling the caller's context, which outlives
	// this session.
	serveCtx, stopServing := context.WithCancel(workerCtx)
	defer stopServing()
	joinScripts := runtime.StartScripts(workerCtx, logger)
	// controlFailures carries the control listener's failure. It is buffered so
	// the control worker never blocks when the Session fails first and the
	// failure goes unread.
	controlFailures := make(chan error, 1)
	var controlDone sync.WaitGroup
	if controlAddr != "" {
		controlFunctions := map[string]*scripted.FunctionRuntime{}
		if len(entry.Functions) > 0 {
			controlFunctions[entry.AdapterID] = functions
		}
		controlDone.Go(func() {
			controlErr := serveSimulatorControl(workerCtx, controlAddr,
				map[string]*scripted.Runtime{"": runtime},
				map[string]*scripted.NotificationRuntime{entry.AdapterID: notifications},
				controlFunctions, logger)
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
	select {
	case failure := <-failures:
		return failure
	default:
	}
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
