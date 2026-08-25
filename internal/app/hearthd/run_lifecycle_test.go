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

	natsserver "github.com/nats-io/nats-server/v2/server"
)

func TestRunCleansUpAfterHTTPServeFailure(t *testing.T) {
	server := startLifecycleNATS(t)
	sentinel := errors.New("serve failed")
	var steps []appShutdownStep
	err := run(context.Background(), Config{
		HTTPAddr: unusedLoopbackAddress(t), NATSURL: server.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), appControls{
		serveHTTP:      func(*http.Server) error { return sentinel },
		onShutdownStep: func(step appShutdownStep) { steps = append(steps, step) },
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v", err)
	}
	assertShutdownSteps(t, steps)
}

func TestRunCleansUpAfterIngressStartupFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		controls func(error, func(appShutdownStep)) appControls
	}{
		{"Registration", func(sentinel error, observe func(appShutdownStep)) appControls {
			return appControls{beforeRegistrationStart: func() error { return sentinel }, onShutdownStep: observe}
		}},
		{"Observation", func(sentinel error, observe func(appShutdownStep)) appControls {
			return appControls{beforeObservationStart: func() error { return sentinel }, onShutdownStep: observe}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := startLifecycleNATS(t)
			sentinel := errors.New("startup failed")
			var steps []appShutdownStep
			err := run(context.Background(), Config{
				HTTPAddr: unusedLoopbackAddress(t), NATSURL: server.ClientURL(),
				SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
			}, slog.New(slog.NewTextHandler(io.Discard, nil)), test.controls(sentinel, func(step appShutdownStep) {
				steps = append(steps, step)
			}))
			if !errors.Is(err, sentinel) {
				t.Fatalf("Run error = %v", err)
			}
			assertShutdownSteps(t, steps)
		})
	}
}

func TestJoinRunErrors(t *testing.T) {
	primary := errors.New("primary")
	first := errors.New("first cleanup")
	second := errors.New("second cleanup")
	if joinRunErrors(nil, nil) != nil {
		t.Fatal("all-nil errors did not return nil")
	}
	joined := joinRunErrors(primary, nil, first, second)
	for _, want := range []error{primary, first, second} {
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

func assertShutdownSteps(t *testing.T, got []appShutdownStep) {
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
