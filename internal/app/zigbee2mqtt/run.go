package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"

	zigbee2mqttadapter "github.com/mholtzscher/hearth/internal/adapters/zigbee2mqtt"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const runtimeComponents = 2

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
		SoftwareName:    "hearth-adapter-zigbee2mqtt",
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

	zigbeeAdapter, err := zigbee2mqttadapter.New(session, zigbee2mqttadapter.Config{
		MQTTURL:   config.MQTT.URL,
		BaseTopic: config.MQTT.BaseTopic,
		ClientID:  DeriveClientID(config.AdapterID),
	}, logger)
	if err != nil {
		return err
	}

	return supervise(
		ctx,
		zigbeeAdapter.Run,
		func(runContext context.Context) error {
			return session.ServeCommands(runContext, zigbeeAdapter.HandleCommand)
		},
		func(functionContext context.Context) error {
			functions, startErr := session.StartFunctionServer(functionContext, zigbeeAdapter.Functions())
			if startErr != nil {
				return startErr
			}
			return runFunctionServer(functionContext, functions)
		},
	)
}

func runFunctionServer(ctx context.Context, functions *adapter.FunctionServer) error {
	waitErr := functions.Wait(ctx)
	closeErr := functions.Close(context.Background())
	requestedClose := errors.Is(waitErr, adapter.ErrClosed) && ctx.Err() != nil
	if waitErr != nil && !requestedClose && !errors.Is(waitErr, context.Canceled) {
		return waitErr
	}
	return closeErr
}

func supervise(
	ctx context.Context,
	runAdapter func(context.Context) error,
	serveCommands func(context.Context) error,
	serveFunctions func(context.Context) error,
) error {
	// Parent cancellation requests ordered shutdown; it must not cancel the
	// runtime dependencies before Function handlers have returned.
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	functionContext, stopFunctions := context.WithCancel(runContext)
	defer stopFunctions()
	results := make(chan error, runtimeComponents)
	functionResult := make(chan error, 1)
	go func() { results <- runAdapter(runContext) }()
	go func() { results <- serveCommands(runContext) }()
	go func() { functionResult <- serveFunctions(functionContext) }()

	var completed []error
	var functionErr error
	select {
	case <-ctx.Done():
		// Keep MQTT and Command serving alive through the Function join.
		stopFunctions()
		joining := true
		for joining {
			select {
			case functionErr = <-functionResult:
				joining = false
			case runErr := <-results:
				completed = append(completed, runErr)
				cancel() // Actual failure during graceful draining interrupts work.
			}
		}
	case first := <-results:
		completed = append(completed, first)
		// A real component failure is not graceful dependency preservation.
		cancel()
		functionErr = <-functionResult
	case functionErr = <-functionResult:
		cancel()
	}
	cancel()
	for len(completed) < runtimeComponents {
		completed = append(completed, <-results)
	}
	completed = append(completed, functionErr)
	for _, runErr := range completed {
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			return runErr
		}
	}
	return nil
}
