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

// entityEventPublisher is the narrow seam the operator input loop needs:
// publishing one validated synthetic Entity Event report for the scenario's
// event source Entity.
type entityEventPublisher interface {
	EmitEntityEvent(context.Context, string) (adapter.EntityEventID, error)
}

// RunEntityEventInput publishes Entity Event names typed on reader until the
// context is canceled or the reader reaches EOF. The handoff holds no queue: a
// name reaches the single publisher worker only while that worker is idle, and
// a name typed while a report is in flight is logged and dropped instead of
// buffered. EOF stops only input; this function joins both workers before
// returning, closing a closable reader to release a blocked read.
func RunEntityEventInput(
	ctx context.Context,
	reader io.Reader,
	publisher entityEventPublisher,
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
		publishEntityEventNames(ctx, names, publisherIdle, publisher, logger)
	})
	workers.Go(func() {
		readEntityEventNames(ctx, reader, names, publisherIdle, logger)
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

// readEntityEventNames reads one whitespace-trimmed name per line and hands it
// to the publisher goroutine. Empty lines are ignored; input received while the
// publisher is publishing is logged and dropped.
func readEntityEventNames(
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
			logDroppedEntityEventInput(ctx, logger, len(name))
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
		logger.InfoContext(ctx, "entity event input reader stopped",
			slog.String("component", "simulator"),
			slog.String("event", "simulator.entity_event_input_stopped"),
		)
	}
}

// publishEntityEventNames publishes every handed-over name in order and returns
// the idle token only after the report is stored. It is the only sender on
// publisherIdle, so the token never queues behind another one.
func publishEntityEventNames(
	ctx context.Context,
	names <-chan string,
	publisherIdle chan<- struct{},
	publisher entityEventPublisher,
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
			publishEntityEventName(ctx, publisher, name, logger)
			publisherIdle <- struct{}{}
		}
	}
}

// publishEntityEventName publishes one synthetic report. Failures are reported
// with a fixed code and the input size only: the typed line is arbitrary
// operator input and never enters a log record.
func publishEntityEventName(
	ctx context.Context,
	publisher entityEventPublisher,
	name string,
	logger *slog.Logger,
) {
	eventID, err := publisher.EmitEntityEvent(ctx, name)
	if err != nil {
		logger.InfoContext(ctx, "entity event input was not published",
			slog.String("component", "simulator"),
			slog.String("event", "simulator.entity_event_input_failed"),
			slog.Int("input_size", len(name)),
			slog.String("error_code", "entity_event_input_failed"),
		)
		return
	}
	logger.DebugContext(ctx, "entity event input published",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.entity_event_published"),
		slog.Int("input_size", len(name)),
		slog.String("entity_event_id", string(eventID)),
	)
}

// logDroppedEntityEventInput records one name dropped because a report was
// already publishing. It carries no queue: the dropped input is not retried.
func logDroppedEntityEventInput(ctx context.Context, logger *slog.Logger, inputSize int) {
	logger.InfoContext(ctx, "dropping entity event input while a report is publishing",
		slog.String("component", "simulator"),
		slog.String("event", "simulator.entity_event_input_dropped"),
		slog.Int("input_size", inputSize),
	)
}
