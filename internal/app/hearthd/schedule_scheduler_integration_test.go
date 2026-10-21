//go:build integration

//nolint:testpackage // Tests exercise the private app worker and shutdown owner.
package hearthd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

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
