package simulator

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// deviceEventPublisher is the narrow seam the operator input loop needs:
// publishing one validated synthetic Device Event report for the scenario's
// event source Entity.
type deviceEventPublisher interface {
	EmitDeviceEvent(context.Context, string) (adapter.DeviceEventID, error)
}

// RunDeviceEventInput publishes Device Event names typed on reader until the
// context is canceled or the reader reaches EOF. The handoff holds no queue: a
// name reaches the single publisher worker only while that worker is idle, and
// a name typed while a report is in flight is logged and dropped instead of
// buffered. EOF stops only input; this function joins both workers before
// returning, closing a closable reader to release a blocked read.
func RunDeviceEventInput(
	ctx context.Context,
	reader io.Reader,
	publisher deviceEventPublisher,
	logger *slog.Logger,
) {
	if logger == nil {
		logger = slog.Default()
	}
	names := make(chan string)
	// One idle token is the entire handoff state: the reader takes it before
	// handing over a name and the publisher returns it only after the report
	// is published, so input during publication drops rather than queues.
	publisherIdle := make(chan struct{}, 1)
	publisherIdle <- struct{}{}
	var workers sync.WaitGroup
	workers.Go(func() {
		publishDeviceEventNames(ctx, names, publisherIdle, publisher, logger)
	})
	workers.Go(func() {
		readDeviceEventNames(ctx, reader, names, publisherIdle, logger)
	})
	// Cancellation cannot interrupt a blocked Read on its own. Closing the
	// reader releases it whenever the source is closable, so shutdown always
	// joins the workers instead of leaking a blocked goroutine.
	readerDone := make(chan struct{})
	go func() {
		closer, ok := reader.(io.Closer)
		if !ok {
			return
		}
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-readerDone:
		}
	}()
	workers.Wait()
	close(readerDone)
}

// readDeviceEventNames reads one whitespace-trimmed name per line and hands it
// to the publisher goroutine. Empty lines are ignored; input received while the
// publisher is publishing is logged and dropped.
func readDeviceEventNames(
	ctx context.Context,
	reader io.Reader,
	names chan<- string,
	publisherIdle <-chan struct{},
	logger *slog.Logger,
) {
	defer close(names)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		select {
		case <-publisherIdle:
		default:
			logDroppedDeviceEventInput(ctx, logger, len(name))
			continue
		}
		select {
		case names <- name:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		// The reader failed for a reason other than shutdown. The error text
		// is not logged because it can embed input or path details.
		logger.InfoContext(ctx, "device event input reader stopped",
			slog.String("component", "simulator"),
			slog.String("event", "simulator.device_event_input_stopped"),
		)
	}
}

// publishDeviceEventNames publishes every handed-over name in order and returns
// the idle token only after the report is stored. It is the only sender on
// publisherIdle, so the token never queues behind another one.
func publishDeviceEventNames(
	ctx context.Context,
	names <-chan string,
	publisherIdle chan<- struct{},
	publisher deviceEventPublisher,
	logger *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case name, ok := <-names:
			if !ok {
				return
			}
			publishDeviceEventName(ctx, publisher, name, logger)
			publisherIdle <- struct{}{}
		}
	}
}

// publishDeviceEventName publishes one synthetic report. Failures are reported
// with a fixed code and the input size only: the typed line is arbitrary
// operator input and never enters a log record.
func publishDeviceEventName(
	ctx context.Context,
	publisher deviceEventPublisher,
	name string,
	logger *slog.Logger,
) {
	eventID, err := publisher.EmitDeviceEvent(ctx, name)
	if err != nil {
		logger.InfoContext(ctx, "device event input was not published",
			slog.String("component", "simulator"),
			slog.String("event", "simulator.device_event_input_failed"),
			slog.Int("input_size", len(name)),
			slog.String("error_code", "device_event_input_failed"),
		)
		return
	}
	logger.DebugContext(ctx, "device event input published",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.device_event_published"),
		slog.Int("input_size", len(name)),
		slog.String("device_event_id", string(eventID)),
	)
}

// logDroppedDeviceEventInput records one name dropped because a report was
// already publishing. It carries no queue: the dropped input is not retried.
func logDroppedDeviceEventInput(ctx context.Context, logger *slog.Logger, inputSize int) {
	logger.InfoContext(ctx, "dropping device event input while a report is publishing",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.device_event_input_dropped"),
		slog.Int("input_size", inputSize),
	)
}
