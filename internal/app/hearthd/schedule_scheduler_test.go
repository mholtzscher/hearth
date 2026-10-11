//nolint:testpackage // Tests exercise the private app worker and shutdown owner.
package hearthd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

type scheduleProcessorStub struct {
	initialize func(context.Context, time.Time) error
	process    func(context.Context) (automations.AdmissionOutcome, error)
	stop       func()
}

func (processor scheduleProcessorStub) InitializeSchedules(ctx context.Context, at time.Time) error {
	return processor.initialize(ctx, at)
}

func (processor scheduleProcessorStub) ProcessDueSchedules(ctx context.Context) (automations.AdmissionOutcome, error) {
	return processor.process(ctx)
}

func (processor scheduleProcessorStub) StopAdmission() { processor.stop() }

func TestScheduleWorkerInitializationFailureIsSynchronous(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("watermark failed")
	at := time.Date(2026, 10, 2, 9, 0, 20, 0, time.FixedZone("household", 3600))
	processor := scheduleProcessorStub{initialize: func(_ context.Context, got time.Time) error {
		if got != at.UTC() {
			t.Fatalf("activation = %v, want %v", got, at.UTC())
		}
		return wantErr
	}}
	worker, err := startScheduleScheduling(context.Background(), nil, processor,
		func() time.Time { return at }, func() (<-chan time.Time, func()) {
			t.Fatal("ticker started after failed initialization")
			return nil, nil
		})
	if worker != nil || !errors.Is(err, wantErr) {
		t.Fatalf("start = %v, %v", worker, err)
	}
}

// Processing waits for ticks, remains serial while blocked, and does not loop
// based on admission outcomes. Cancellation joins the call and stops the ticker.
//
//nolint:gocognit // One synchronized sequence protects worker ordering through shutdown.
func TestScheduleWorkerSerialTicksAndCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ticks := make(chan time.Time, 1)
		release := make(chan struct{})
		initialized, tickerStopped, calls := false, false, 0
		processor := scheduleProcessorStub{
			initialize: func(context.Context, time.Time) error { initialized = true; return nil },
			process: func(ctx context.Context) (automations.AdmissionOutcome, error) {
				calls++
				select {
				case <-release:
					return automations.AdmissionOutcome{StartedRuns: 100}, nil
				case <-ctx.Done():
					return automations.AdmissionOutcome{}, ctx.Err()
				}
			},
			stop: func() { t.Error("normal cancellation closed admission as a worker fault") },
		}
		worker, err := startScheduleScheduling(context.Background(), slog.New(slog.DiscardHandler), processor,
			time.Now, func() (<-chan time.Time, func()) {
				if !initialized {
					t.Error("ticker created before initialization")
				}
				return ticks, func() { tickerStopped = true }
			})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if calls != 0 {
			t.Fatal("processing before first tick")
		}
		ticks <- time.Time{} // A stale ticker value cannot dictate evaluation time.
		synctest.Wait()
		ticks <- time.Time{}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("overlapping calls = %d", calls)
		}
		release <- struct{}{}
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("second tick calls = %d", calls)
		}
		release <- struct{}{}
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("worker drained a synthetic batch: calls = %d", calls)
		}
		ticks <- time.Time{}
		synctest.Wait()
		if calls != 3 {
			t.Fatalf("in-flight cancellation calls = %d", calls)
		}
		if stopErr := worker.Stop(context.Background()); stopErr != nil {
			t.Fatal(stopErr)
		}
		if !tickerStopped {
			t.Fatal("ticker was not stopped")
		}
	})
}

func TestScheduleWorkerFailureClosesAdmissionAndFailsReadiness(t *testing.T) {
	t.Parallel()
	fixture := newReadinessFixture(t)
	wantErr := errors.New("schedule transaction failed")
	service := automations.NewService(nil, nil, automations.Dependencies{})
	ticks := make(chan time.Time, 1)
	processor := scheduleProcessorStub{
		initialize: func(context.Context, time.Time) error { return nil },
		process: func(context.Context) (automations.AdmissionOutcome, error) {
			return automations.AdmissionOutcome{}, wantErr
		},
		stop: service.StopAdmission,
	}
	worker, err := startScheduleScheduling(context.Background(), slog.New(slog.DiscardHandler), processor,
		time.Now, func() (<-chan time.Time, func()) { return ticks, func() {} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	// Put the calendar worker second to protect the variadic readiness wiring.
	held := lifecycle.StartWorker(context.Background(), func(ctx context.Context) error { <-ctx.Done(); return nil })
	t.Cleanup(func() { _ = held.Stop(context.Background()) })
	fixture.readiness.automationWorkers = []*lifecycle.WorkerHandle{held, worker}
	if readinessErr := fixture.readiness.Check(context.Background()); readinessErr != nil {
		t.Fatalf("healthy calendar readiness = %v", readinessErr)
	}
	ticks <- time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if workerErr := worker.Wait(ctx); !errors.Is(workerErr, wantErr) {
		t.Fatalf("worker failure = %v", workerErr)
	}
	if service.AdmissionOpen() {
		t.Fatal("failed calendar left Automation admission open")
	}
	if readinessErr := fixture.readiness.Check(context.Background()); readinessErr == nil {
		t.Fatal("failed calendar left readiness healthy")
	}
	shutdown := &coreShutdown{heldStateWorker: held, scheduleWorker: worker}
	serveErr := serveHTTP(context.Background(), Config{HTTPAddr: "127.0.0.1:0"}, shutdown,
		http.NewServeMux(), slog.New(slog.DiscardHandler), nil)
	t.Cleanup(func() { _ = shutdown.server.Close() })
	if ErrorStage(serveErr) != "automation_schedule" || !errors.Is(serveErr, wantErr) {
		t.Fatalf("HTTP worker-death path = %v", serveErr)
	}
}

func TestScheduleWorkerUnavailableAdmissionIsUnexpectedFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ticks := make(chan time.Time, 1)
		ticks <- time.Now()
		stopped := false
		processor := scheduleProcessorStub{
			initialize: func(context.Context, time.Time) error { return nil },
			process: func(context.Context) (automations.AdmissionOutcome, error) {
				return automations.AdmissionOutcome{}, automations.ErrAdmissionUnavailable
			},
			stop: func() { stopped = true },
		}
		worker, err := startScheduleScheduling(context.Background(), slog.New(slog.DiscardHandler), processor,
			time.Now, func() (<-chan time.Time, func()) { return ticks, func() {} })
		if err != nil {
			t.Fatal(err)
		}
		if workerErr := worker.Wait(
			context.Background(),
		); !errors.Is(workerErr, automations.ErrAdmissionUnavailable) ||
			!stopped {
			t.Fatalf("unavailable admission = %v, stopped = %v", workerErr, stopped)
		}
	})
}

func TestShutdownStopsCalendarBeforeClosingAutomationAdmission(t *testing.T) {
	t.Parallel()
	service := automations.NewService(nil, nil, automations.Dependencies{})
	stopped := make(chan bool, 1)
	worker := lifecycle.StartWorker(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		stopped <- service.AdmissionOpen()
		return nil
	})
	shutdown := &coreShutdown{
		runContext: context.Background(), logger: slog.New(slog.DiscardHandler),
		scheduleWorker: worker, automationService: service,
	}
	if err := shutdown.run(); err != nil {
		t.Fatal(err)
	}
	if !<-stopped || service.AdmissionOpen() {
		t.Fatal("calendar was not joined before closing Automation admission")
	}
}
