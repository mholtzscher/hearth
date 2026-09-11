package simulator_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// gatedPublisher blocks inside one publication and signals that it has started,
// so a test can prove input received while a report is in flight is dropped
// rather than queued or deferred.
type gatedPublisher struct {
	entered chan string
	release chan struct{}

	mutex     sync.Mutex
	published []string
}

func newGatedPublisher() *gatedPublisher {
	return &gatedPublisher{entered: make(chan string, 1), release: make(chan struct{})}
}

func (publisher *gatedPublisher) EmitEntityEvent(
	_ context.Context,
	name string,
) (adapter.EntityEventID, error) {
	publisher.entered <- name
	<-publisher.release
	publisher.mutex.Lock()
	publisher.published = append(publisher.published, name)
	publisher.mutex.Unlock()
	return "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

func (publisher *gatedPublisher) names() []string {
	publisher.mutex.Lock()
	defer publisher.mutex.Unlock()
	return append([]string(nil), publisher.published...)
}

// recordingPublisher records every published name immediately and reports a
// configured failure for one name.
type recordingPublisher struct {
	published chan string
	failFor   string
}

func (publisher *recordingPublisher) EmitEntityEvent(
	_ context.Context,
	name string,
) (adapter.EntityEventID, error) {
	if name == publisher.failFor {
		return "", errors.New("simulated publication failure")
	}
	publisher.published <- name
	return "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil
}

// logRecorder is a concurrency-safe slog handler that keeps the structured
// events emitted by the input loops.
type logRecorder struct {
	mutex   sync.Mutex
	records []slog.Record
}

func newLogRecorder() (*slog.Logger, *logRecorder) {
	recorder := &logRecorder{}
	return slog.New(recorder), recorder
}

func (recorder *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (recorder *logRecorder) Handle(_ context.Context, record slog.Record) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.records = append(recorder.records, record)
	return nil
}

func (recorder *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return recorder }

func (recorder *logRecorder) WithGroup(string) slog.Handler { return recorder }

func (recorder *logRecorder) count(event string) int {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	total := 0
	for _, record := range recorder.records {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "event" && attr.Value.String() == event {
				total++
			}
			return true
		})
	}
	return total
}

func waitForInputCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatal("timed out waiting for entity event input condition")
	}
}

// This test protects the no-queue operator input contract and fails if a name
// typed while a report is publishing is buffered, retried, or published.
func TestRunEntityEventInputDropsInputWhilePublisherIsBusy(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger, recorder := newLogRecorder()
	publisher := newGatedPublisher()
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		simulatorapp.RunEntityEventInput(ctx, reader, publisher, logger)
	}()

	writeLine(t, writer, simulatoradapter.EntityEventSinglePress)
	var inFlight string
	select {
	case inFlight = <-publisher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first input line did not reach the publisher")
	}
	if inFlight != simulatoradapter.EntityEventSinglePress {
		t.Fatalf("in-flight name = %q", inFlight)
	}
	writeLine(t, writer, simulatoradapter.EntityEventDoublePress)
	writeLine(t, writer, simulatoradapter.EntityEventSinglePress)
	waitForInputCondition(t, 5*time.Second, func() bool {
		return recorder.count("simulator.entity_event_input_dropped") == 2
	})
	if published := publisher.names(); len(published) != 0 {
		t.Fatalf("input published before release: %#v", published)
	}
	close(publisher.release)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("input workers did not stop after EOF")
	}
	if published := publisher.names(); len(published) != 1 || published[0] != simulatoradapter.EntityEventSinglePress {
		t.Fatalf("published names = %#v", published)
	}
}

// This test protects EOF and cancellation shutdown and fails if either leaves
// the workers running or drops the already-handed-over name.
func TestRunEntityEventInputStopsOnEOFAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("EOF stops only input", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger, _ := newLogRecorder()
		publisher := &recordingPublisher{published: make(chan string, 4)}
		reader, writer := io.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			simulatorapp.RunEntityEventInput(ctx, reader, publisher, logger)
		}()
		writeLine(t, writer, simulatoradapter.EntityEventSinglePress)
		select {
		case name := <-publisher.published:
			if name != simulatoradapter.EntityEventSinglePress {
				t.Fatalf("published name = %q", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("typed name was not published")
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("EOF did not stop the input workers")
		}
	})
	t.Run("cancellation releases a blocked reader", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		logger, _ := newLogRecorder()
		publisher := &recordingPublisher{published: make(chan string, 1)}
		reader, writer := io.Pipe()
		defer func() { _ = writer.Close() }()
		done := make(chan struct{})
		go func() {
			defer close(done)
			simulatorapp.RunEntityEventInput(ctx, reader, publisher, logger)
		}()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation did not join the blocked reader")
		}
	})
}

// This test protects the failure path and fails if an unpublished name is
// reported as published or stops later input from being handled.
func TestRunEntityEventInputReportsPublicationFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger, recorder := newLogRecorder()
	publisher := &recordingPublisher{published: make(chan string, 4), failFor: simulatoradapter.EntityEventDoublePress}
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		simulatorapp.RunEntityEventInput(ctx, reader, publisher, logger)
	}()
	writeLine(t, writer, simulatoradapter.EntityEventDoublePress)
	waitForInputCondition(t, 5*time.Second, func() bool {
		return recorder.count("simulator.entity_event_input_failed") == 1
	})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("input workers did not stop after EOF")
	}
	select {
	case name := <-publisher.published:
		t.Fatalf("failed name was published: %q", name)
	default:
	}
}

func writeLine(t *testing.T, writer io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		t.Fatal(err)
	}
}
