//go:build integration

//nolint:testpackage // Tests exercise the package-private shutdown ordering of runScriptedSession.
package simulator

import (
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// freeLoopbackAddr reserves and releases one loopback port so the control
// channel can be dialed and, after shutdown, rebound.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return addr
}

// This test protects startup failure when the configured control listener
// cannot bind while the caller's context stays live. It fails if
// runScriptedSession keeps serving Commands after the listener error: the
// process then looks initialized without its requested control API and startup
// waits out its whole readiness budget.
func TestRunScriptedSessionReturnsControlListenerFailure(t *testing.T) {
	t.Parallel()
	// An address with no port fails to parse on every platform, so the listener
	// failure is deterministic instead of racing another process for a port.
	const invalidControlAddr = "invalid-control-address"
	ctx := t.Context()
	session := newFencingSession()
	entry := ScriptedAdapterConfig{AdapterID: "simulator", Devices: []scripted.DeviceSpec{tickingPowerDevice()}}
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runScriptedSession(ctx, entry, session, slog.New(slog.DiscardHandler), invalidControlAddr)
	}()
	var runErr error
	select {
	case runErr = <-runErrors:
	case <-time.After(sessionFailureWait):
		t.Fatal("runScriptedSession kept serving after the control listener failed")
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "listen on control channel") {
		t.Fatalf("runScriptedSession error = %v, want the control listener failure", runErr)
	}
	if !strings.Contains(runErr.Error(), invalidControlAddr) {
		t.Fatalf("runScriptedSession error %q omits the control address %q", runErr, invalidControlAddr)
	}
	if ctx.Err() != nil {
		t.Fatalf("shutdown cancelled the caller's context: %v", ctx.Err())
	}
	if !session.isClosed() {
		t.Fatal("Session was not closed after shutdown")
	}
	if session.publishedAfterSessionClose() {
		t.Fatal("publication reached the Session after it closed")
	}
	// runScriptedSession joins the ticker workers before returning, so no
	// publication may follow the return.
	published := session.publications()
	time.Sleep(4 * tickInterval)
	if after := session.publications(); after != published {
		t.Fatalf("ticker published %d more values after shutdown returned", after-published)
	}
}

// This test protects bounded shutdown when the Session fails while an Entity
// ticker and the control channel are running and the caller's context stays
// live. It fails if runScriptedSession joins the workers on the caller's
// context: the deferred join blocks forever instead of returning the fencing
// error, the control port stays bound, and the Session never closes.
func TestRunScriptedSessionReturnsSessionErrorWithControlAndTicker(t *testing.T) {
	t.Parallel()
	controlAddr := freeLoopbackAddr(t)
	session, runErr := runScriptedSessionUntilFenced(t, controlAddr)
	if !errors.Is(runErr, adapter.ErrRuntimeFenced) {
		t.Fatalf("runScriptedSession error = %v, want %v", runErr, adapter.ErrRuntimeFenced)
	}
	if !session.isClosed() {
		t.Fatal("Session was not closed after shutdown")
	}
	if session.publishedAfterSessionClose() {
		t.Fatal("publication reached the Session after it closed")
	}
	listener, listenErr := net.Listen("tcp", controlAddr)
	if listenErr != nil {
		t.Fatalf("control channel still bound %s after shutdown: %v", controlAddr, listenErr)
	}
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}
