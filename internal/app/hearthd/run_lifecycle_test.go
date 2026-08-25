package hearthd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

func TestRunCleansUpAfterHTTPServeFailure(t *testing.T) {
	server := startLifecycleNATS(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	sentinel := errors.New("serve failure")
	var steps []appShutdownStep
	controls := productionAppControls()
	controls.serveHTTP = func(*http.Server) error { return sentinel }
	controls.onShutdownStep = func(step appShutdownStep) { steps = append(steps, step) }

	err := run(context.Background(), Config{
		HTTPAddr: unusedLoopbackAddress(t), NATSURL: server.ClientURL(), SQLitePath: databasePath,
	}, discardLifecycleLogger(), controls)
	if !errors.Is(err, sentinel) {
		t.Fatalf("run error = %v, want serve sentinel", err)
	}
	assertLifecycleShutdownSteps(t, steps)
	assertLifecycleDatabaseReopens(t, databasePath)
}

func TestRunCleansUpAfterIngressStartupFailure(t *testing.T) {
	for _, stage := range []string{"Registration", "Observation"} {
		t.Run(stage, func(t *testing.T) {
			server := startLifecycleNATS(t)
			databasePath := filepath.Join(t.TempDir(), "hearth.db")
			sentinel := errors.New(stage + " startup failure")
			var steps []appShutdownStep
			controls := productionAppControls()
			controls.serveHTTP = func(*http.Server) error {
				t.Fatal("HTTP started after ingress startup failed")
				return nil
			}
			if stage == "Registration" {
				controls.beforeRegistrationStart = func() error { return sentinel }
			} else {
				controls.beforeObservationStart = func() error { return sentinel }
			}
			controls.onShutdownStep = func(step appShutdownStep) { steps = append(steps, step) }

			err := run(context.Background(), Config{
				HTTPAddr: unusedLoopbackAddress(t), NATSURL: server.ClientURL(), SQLitePath: databasePath,
			}, discardLifecycleLogger(), controls)
			if !errors.Is(err, sentinel) {
				t.Fatalf("run error = %v, want startup sentinel", err)
			}
			assertLifecycleShutdownSteps(t, steps)
			assertLifecycleDatabaseReopens(t, databasePath)
		})
	}
}

func TestRunJoinsModuleAndIngressBeforeClosingResources(t *testing.T) {
	server := startLifecycleNATS(t)
	var steps []appShutdownStep
	controls := productionAppControls()
	controls.serveHTTP = func(*http.Server) error { return errors.New("stop after startup") }
	controls.onShutdownStep = func(step appShutdownStep) { steps = append(steps, step) }
	_ = run(context.Background(), Config{
		HTTPAddr: unusedLoopbackAddress(t), NATSURL: server.ClientURL(), SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, discardLifecycleLogger(), controls)
	assertLifecycleShutdownSteps(t, steps)
}

func TestJoinRunErrors(t *testing.T) {
	primary := errors.New("primary")
	firstCleanup := errors.New("first cleanup")
	secondCleanup := errors.New("second cleanup")

	if err := joinRunErrors(nil, nil); err != nil {
		t.Fatalf("all-nil result = %v", err)
	}
	joined := joinRunErrors(primary, nil, firstCleanup, secondCleanup)
	for _, want := range []error{primary, firstCleanup, secondCleanup} {
		if !errors.Is(joined, want) {
			t.Fatalf("joined error %v does not retain %v", joined, want)
		}
	}
}

func startLifecycleNATS(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func discardLifecycleLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func assertLifecycleShutdownSteps(t *testing.T, got []appShutdownStep) {
	t.Helper()
	want := []appShutdownStep{
		shutdownIngressQuiesced,
		shutdownModuleCanceled,
		shutdownModuleJoined,
		shutdownIngressJoined,
		shutdownNATSClosed,
		shutdownDatabaseClosed,
	}
	if len(got) != len(want) {
		t.Fatalf("shutdown steps = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("shutdown steps = %v, want %v", got, want)
		}
	}
}

func assertLifecycleDatabaseReopens(t *testing.T, path string) {
	t.Helper()
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}
