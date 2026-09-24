//nolint:testpackage // These tests exercise the app-owned private scheduler.
package hearthd

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type heldStateProcessorStub struct {
	results chan heldStateProcessResult
	calls   chan heldStateProcessCall
	stopped chan struct{}
}

type heldStateProcessResult struct {
	processed int
	err       error
}

type heldStateProcessCall struct {
	at    time.Time
	limit int
}

func (processor *heldStateProcessorStub) ProcessDueHeldStates(
	_ context.Context,
	at time.Time,
	limit int,
) (int, error) {
	processor.calls <- heldStateProcessCall{at: at, limit: limit}
	result := <-processor.results
	return result.processed, result.err
}

func (processor *heldStateProcessorStub) StopAdmission() {
	close(processor.stopped)
}

func TestHeldStateSchedulerWaitsForTickAndDrainsFullBatches(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time)
	results := make(chan heldStateProcessResult, 2)
	results <- heldStateProcessResult{processed: heldStateBatchLimit}
	results <- heldStateProcessResult{processed: 4}
	processor := &heldStateProcessorStub{
		results: results, calls: make(chan heldStateProcessCall, 2), stopped: make(chan struct{}),
	}
	instant := time.Date(2026, time.September, 23, 12, 0, 0, 0, time.FixedZone("test", 3600))
	worker := startHeldStateScheduling(
		context.Background(), slog.New(slog.DiscardHandler), processor,
		func() time.Time { return instant },
		func() (<-chan time.Time, func()) { return ticks, func() {} },
	)
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	select {
	case call := <-processor.calls:
		t.Fatalf("scheduler processed before the first tick: %#v", call)
	default:
	}
	ticks <- instant.Add(time.Second)
	for range 2 {
		select {
		case call := <-processor.calls:
			if call.at != instant.UTC() || call.limit != heldStateBatchLimit {
				t.Fatalf("ProcessDueHeldStates call = %#v", call)
			}
		case <-time.After(time.Second):
			t.Fatal("scheduler did not drain due batches")
		}
	}
	select {
	case call := <-processor.calls:
		t.Fatalf("scheduler spun after a short batch: %#v", call)
	default:
	}

	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("worker Stop() error = %v", err)
	}
}

func TestHeldStateSchedulerFailureStopsAdmissionAndTerminates(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	wantErr := errors.New("storage failed")
	processor := &heldStateProcessorStub{
		results: make(chan heldStateProcessResult, 1),
		calls:   make(chan heldStateProcessCall, 1),
		stopped: make(chan struct{}),
	}
	processor.results <- heldStateProcessResult{err: wantErr}
	worker := startHeldStateScheduling(
		context.Background(), slog.New(slog.DiscardHandler), processor, time.Now,
		func() (<-chan time.Time, func()) { return ticks, func() {} },
	)
	select {
	case <-processor.stopped:
	case <-time.After(time.Second):
		t.Fatal("scheduler failure did not close admission")
	}
	if err := worker.Wait(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("worker Wait() error = %v, want %v", err, wantErr)
	}
}

// Closing automation admission elsewhere leaves Core serving while the
// scheduler waits for ticks; it must not report a second worker fault.
func TestHeldStateSchedulerWaitsWhenAdmissionIsUnavailable(t *testing.T) {
	t.Parallel()
	ticks := make(chan time.Time)
	processor := &heldStateProcessorStub{
		results: make(chan heldStateProcessResult, 2),
		calls:   make(chan heldStateProcessCall, 2),
		stopped: make(chan struct{}),
	}
	for range 2 {
		processor.results <- heldStateProcessResult{err: automations.ErrAdmissionUnavailable}
	}
	worker := startHeldStateScheduling(
		context.Background(), slog.New(slog.DiscardHandler), processor, time.Now,
		func() (<-chan time.Time, func()) { return ticks, func() {} },
	)
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	for range 2 {
		select {
		case ticks <- time.Now():
		case <-time.After(time.Second):
			t.Fatal("scheduler stopped before the next tick")
		}
		select {
		case <-processor.calls:
		case <-time.After(time.Second):
			t.Fatal("scheduler did not process a tick")
		}
	}
	if err := worker.Stop(context.Background()); err != nil {
		t.Fatalf("unavailable admission terminated scheduler: %v", err)
	}
	select {
	case <-processor.stopped:
		t.Fatal("scheduler closed an already-unavailable admission gate")
	default:
	}
}
