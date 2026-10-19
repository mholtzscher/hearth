package zigbee2mqtt //nolint:testpackage // Exercises application-owned shutdown ordering.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type testFunctionServer struct {
	closed    chan struct{}
	failure   chan error
	closeHook func()
}

func newTestFunctionServer() *testFunctionServer {
	return &testFunctionServer{closed: make(chan struct{}), failure: make(chan error, 1)}
}
func (server *testFunctionServer) run(ctx context.Context) error {
	err := server.Wait(ctx)
	_ = server.Close(context.Background())
	if errors.Is(err, adapter.ErrClosed) {
		return nil
	}
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
