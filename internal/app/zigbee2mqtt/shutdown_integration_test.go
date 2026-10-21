//go:build integration

package zigbee2mqtt //nolint:testpackage // Exercises application-owned shutdown ordering.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestSupervisePreservesUnexpectedSDKFunctionClosure(t *testing.T) {
	t.Parallel()
	for name, requested := range map[string]bool{"unexpected closure": false, "requested shutdown": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertSDKFunctionClosure(t, requested)
		})
	}
}

func assertSDKFunctionClosure(t *testing.T, requested bool) {
	t.Helper()
	session := newFunctionSupervisionSession(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dependencies := make(chan struct{}, runtimeComponents)
	idle := func(ctx context.Context) error {
		dependencies <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	started := make(chan *adapter.FunctionServer, 1)
	done := make(chan error, 1)
	go func() {
		done <- supervise(ctx, idle, idle, func(ctx context.Context) error {
			server, err := session.StartFunctionServer(ctx, []adapter.Function{{
				Descriptor: adapter.FunctionDescriptor{
					Key: "test", Description: "Test Function supervision", TimeoutMS: 1000,
					InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
				},
				Handler: func(context.Context, adapter.FunctionRequest) adapter.FunctionResult {
					return adapter.FunctionSucceeded{Result: json.RawMessage(`{}`)}
				},
			}})
			if err != nil {
				return err
			}
			started <- server
			return runFunctionServer(ctx, server)
		})
	}()
	var server *adapter.FunctionServer
	select {
	case server = <-started:
	case err := <-done:
		t.Fatalf("Function server did not start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Function server did not start")
	}
	for range runtimeComponents {
		<-dependencies
	}
	if requested {
		cancel()
	} else if err := server.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if requested && err != nil {
			t.Fatalf("requested shutdown failed: %v", err)
		}
		if !requested && !errors.Is(err, adapter.ErrClosed) {
			t.Fatalf("unexpected SDK closure was swallowed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not join SDK closure")
	}
}

func newFunctionSupervisionSession(t *testing.T) *adapter.Session {
	t.Helper()
	broker := startTestNATSServer(t)
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "functions.db"))
	repository := adapterssqlite.NewAdapterRepository(database)
	runtimes := adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close(context.Background()) })
	functions, err := adapters.NewFunctionService(
		t.Context(),
		repository,
		adaptersnats.NewFunctionInvoker(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = functions.Drain(context.Background()) })
	registrations, err := adaptersnats.StartFunctionRegistrationServer(connection, validator, functions, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Close(context.Background()) })
	session, err := adapter.Connect(t.Context(), adapter.Config{
		AdapterID:       "supervision",
		SoftwareName:    "supervision-test",
		SoftwareVersion: "test",
		NATSURL:         broker.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
