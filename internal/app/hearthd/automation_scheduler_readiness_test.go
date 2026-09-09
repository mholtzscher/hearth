package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// lifecycleHTTPStatus polls with throwaway connections. The shared polling
// helpers reuse pooled keep-alive connections that can linger on the test
// server and stall its five-second shutdown drain, so this process-lifecycle
// test never pools.
func lifecycleHTTPStatus(ctx context.Context, t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	response, err := client.Do(request)
	if err != nil {
		return -1
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func waitForLifecycleHealthz(ctx context.Context, t *testing.T, url string, runErrors <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				t.Fatal(runErr)
			}
			t.Fatal(context.Canceled)
		default:
		}
		if lifecycleHTTPStatus(ctx, t, url) == http.StatusOK {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for healthz: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for healthz")
}

func waitForLifecycleReadyz(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if lifecycleHTTPStatus(ctx, t, url) == http.StatusOK {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for readyz: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for readyz")
}

// stubSchedulerHealth is the narrow RuntimeReadiness scheduler seam for tests.
// The zero value reports unhealthy so forgotten initialization fails closed.
type stubSchedulerHealth struct {
	healthy bool
}

func (stub *stubSchedulerHealth) AutomationSchedulerHealthy() bool {
	return stub != nil && stub.healthy
}

// C3: scheduler health is a readiness input alongside the device
// dependencies. A missing scheduler fails closed so unwired assembly can never
// report ready.
func TestRuntimeReadinessRequiresSchedulerHealth(t *testing.T) {
	t.Parallel()
	t.Run("unhealthy", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.scheduler.healthy = false
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with unhealthy scheduler")
		}
	})
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		missing := NewRuntimeReadiness(
			fixture.readiness.database,
			fixture.connection,
			fixture.jetstream,
			fixture.consumer,
			nil,
		)
		if err := missing.Check(context.Background()); err == nil {
			t.Fatal("readiness passed without scheduler health")
		}
	})
}

// C3: /readyz degrades when the scheduler gate closes, without weakening the
// existing execution-admission or direct-command gates.
func TestReadyzRequiresSchedulerReadiness(t *testing.T) {
	t.Parallel()
	stub := &stubHTTPAutomations{}
	devicesStub := &stubDevices{}
	handler, _ := NewHTTPHandler(devicesStub, stub, testHTTPAutomationCodec(t), &testReadiness{}, devicesStub)
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	stub.schedulerUnavailable = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("scheduler-draining status = %d, body = %s", response.Code, response.Body.String())
	}
	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("health status while scheduler-gated = %d", response.Code)
	}
	stub.schedulerUnavailable = false
	stub.unavailable = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("execution-draining status = %d, body = %s", response.Code, response.Body.String())
	}
	stub.unavailable = false
	devicesStub.admissionClosed = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("command-draining status = %d, body = %s", response.Code, response.Body.String())
	}
}

// C3: /readyz tracks real scheduler initialization. A fresh service has open
// execution admission but unhealthy scheduler state, so readiness waits for the
// synchronous startup init that Run performs before serving.
func TestReadyzTracksSchedulerInitialization(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(database, catalog)
	deviceService := devices.NewService(
		devices.SQLiteStores(records),
		nil,
		catalog,
		devices.Dependencies{},
	)
	service := automations.NewService(
		automations.NewSQLiteRepository(database),
		deviceService,
		records,
		testHTTPAutomationCodec(t),
		time.UTC,
		nil,
		automations.WithSchedulerWakeup(make(chan struct{})),
	)
	t.Cleanup(func() {
		service.StopAutomationExecutionAdmission()
		service.StopAutomationScheduler()
	})
	if !service.AutomationExecutionReady() {
		t.Fatal("fresh service closed execution admission")
	}
	if service.AutomationSchedulerReady() {
		t.Fatal("uninitialized scheduler reported ready")
	}
	handler, _ := NewHTTPHandler(
		&stubDevices{},
		service,
		testHTTPAutomationCodec(t),
		&testReadiness{},
		&stubDevices{},
	)
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatal("uninitialized scheduler did not degrade readiness")
	}
	if err = service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("initialized scheduler status = %d, body = %s", response.Code, response.Body.String())
	}
}

// C3: scheduler init failure touches only scheduler health. Execution admission
// stays open so the failure is visible in readiness without inventing an
// executor fault or closing manual admission.
func TestSchedulerInitFailureIsIndependentOfExecutionAdmission(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(database, catalog)
	deviceService := devices.NewService(
		devices.SQLiteStores(records),
		nil,
		catalog,
		devices.Dependencies{},
	)
	service := automations.NewService(
		automations.NewSQLiteRepository(database),
		deviceService,
		records,
		testHTTPAutomationCodec(t),
		nil,
		nil,
	)
	if err = service.StartAutomationScheduler(t.Context()); err == nil {
		t.Fatal("scheduler started without a household timezone")
	}
	if service.AutomationSchedulerHealthy() || service.AutomationSchedulerReady() {
		t.Fatal("failed scheduler init reported healthy")
	}
	if !service.AutomationExecutionReady() {
		t.Fatal("scheduler init failure closed execution admission")
	}
}

// C3: the shared shutdown helper closes execution admission, then stops and
// joins the scheduler loop, before worker drain. Restarting the scheduler after
// the join proves the loop was joined, not merely signaled.
func TestJoinAdmittedExecutionStopsSchedulerBeforeWorkerDrain(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	records := devices.NewSQLiteRepository(database, catalog)
	deviceService := devices.NewService(
		devices.SQLiteStores(records),
		nil,
		catalog,
		devices.Dependencies{},
	)
	service := automations.NewService(
		automations.NewSQLiteRepository(database),
		deviceService,
		records,
		testHTTPAutomationCodec(t),
		time.UTC,
		nil,
		automations.WithSchedulerWakeup(make(chan struct{})),
	)
	if err = service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	joinAdmittedExecution(deviceService, service)
	if service.AutomationExecutionReady() || service.AutomationSchedulerReady() {
		t.Fatal("join left admission or scheduler ready")
	}
	if deviceService.CommandAdmissionOpen() {
		t.Fatal("join left direct command admission open")
	}
	// A second start succeeds only when the first loop was stopped and joined.
	if err = service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatalf("scheduler did not join during shutdown: %v", err)
	}
	service.StopAutomationExecutionAdmission()
	service.StopAutomationScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = service.WaitAutomationRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if err = deviceService.WaitCommands(ctx); err != nil {
		t.Fatal(err)
	}
}

// C3: the assembled process starts the scheduler after dependencies and
// recovery, serves readiness only once scheduler health holds, and shuts down
// cleanly. The wakeup replaces the production timer; C4 owns tick assertions.
func TestCoreSchedulerStartupReadinessAndShutdown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			HouseholdTimezone: "UTC",
			HTTPAddr:          httpAddress,
			NATSURL:           server.ClientURL(),
			SQLitePath:        databasePath,
		}, slog.New(slog.DiscardHandler), WithSchedulerWakeup(make(chan struct{})))
	}()
	waitForLifecycleHealthz(ctx, t, "http://"+httpAddress+"/healthz", runErrors)
	waitForLifecycleReadyz(ctx, t, "http://"+httpAddress+"/readyz")
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("process did not shut down")
	}
}

// C3: an invalid household timezone fails process startup before any scheduler
// or transport work, preserving the existing staged diagnostics.
func TestRunRejectsInvalidHouseholdTimezone(t *testing.T) {
	t.Parallel()
	err := Run(
		context.Background(),
		Config{
			HouseholdTimezone: "Mars/Olympus",
			HTTPAddr:          "127.0.0.1:8080",
			NATSURL:           "nats://127.0.0.1:4222",
			SQLitePath:        filepath.Join(t.TempDir(), "hearth.db"),
		},
		slog.New(slog.DiscardHandler),
	)
	if err == nil {
		t.Fatal("process started with an invalid household timezone")
	}
	if stage := ErrorStage(err); stage != "validate_config" {
		t.Fatalf("error stage = %q, want validate_config", stage)
	}
}
