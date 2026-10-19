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

type testFunctionServer struct {
	closed    chan struct{}
	failure   chan error
	closeHook func()
}

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

func newTestFunctionServer() *testFunctionServer {
	return &testFunctionServer{closed: make(chan struct{}), failure: make(chan error, 1)}
}
func (server *testFunctionServer) run(ctx context.Context) error {
	err := server.Wait(ctx)
	_ = server.Close(context.Background())
	return err
}

func (server *testFunctionServer) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-server.closed:
		return adapter.ErrClosed
	case err := <-server.failure:
		return err
	}
}

func TestSuperviseFunctionFailureCancelsAndJoinsDependencies(t *testing.T) {
	t.Parallel()
	server := newTestFunctionServer()
	started := make(chan struct{}, 2)
	stopped := make(chan struct{}, 2)
	run := func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- supervise(context.Background(), run, run, server.run) }()
	<-started
	<-started
	server.failure <- adapter.ErrRuntimeFenced
	select {
	case err := <-done:
		if !errors.Is(err, adapter.ErrRuntimeFenced) {
			t.Fatalf("function failure = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Function failure did not stop supervisor")
	}
	if len(stopped) != 2 {
		t.Fatal("Function failure did not join runtime and Commands")
	}
}
func (server *testFunctionServer) Close(context.Context) error {
	if server.closeHook != nil {
		server.closeHook()
	}
	close(server.closed)
	return nil
}

// Parent cancellation must not pull MQTT out from under an admitted Function.
func TestSuperviseDrainsFunctionsBeforeCancelingDependencies(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtimeStarted := make(chan context.Context, 1)
	commandStarted := make(chan context.Context, 1)
	closing := make(chan struct{})
	release := make(chan struct{})
	server := newTestFunctionServer()
	server.closeHook = func() { close(closing); <-release }
	done := make(chan error, 1)
	go func() {
		done <- supervise(ctx, func(ctx context.Context) error { runtimeStarted <- ctx; <-ctx.Done(); return ctx.Err() },
			func(ctx context.Context) error { commandStarted <- ctx; <-ctx.Done(); return ctx.Err() }, server.run)
	}()
	runtimeContext, commandContext := <-runtimeStarted, <-commandStarted
	cancel()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("Function admission did not close")
	}
	if runtimeContext.Err() != nil || commandContext.Err() != nil {
		t.Error("dependencies canceled before Function handlers joined")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not join components")
	}
	if runtimeContext.Err() == nil || commandContext.Err() == nil {
		t.Fatal("runtime dependencies were not canceled after join")
	}
}
