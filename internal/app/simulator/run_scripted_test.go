//nolint:testpackage // Tests exercise the package-private shutdown ordering of runScriptedSession.
package simulator

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// sessionFailureWait bounds every synchronization point so a shutdown that
	// joins on the caller's context fails the test instead of hanging it.
	sessionFailureWait = 5 * time.Second
	// tickInterval keeps an Entity ticker busy while the Session fails.
	tickInterval = 20 * time.Millisecond
)

// fencingSession is a scriptedSession whose ServeCommands fails with a Session
// error while the caller's context stays live. The real SDK returns exactly
// this error when the Adapter runtime is fenced, so the fake reproduces the
// path that used to hang shutdown.
type fencingSession struct {
	mutex               sync.Mutex
	closed              bool
	publishedAfterClose bool
	entered             chan struct{}
	fence               chan struct{}
}

func newFencingSession() *fencingSession {
	return &fencingSession{
		entered: make(chan struct{}),
		fence:   make(chan struct{}),
	}
}

func (session *fencingSession) Register(
	_ context.Context,
	registration adapter.Registration,
) (adapter.Binding, error) {
	binding := adapter.Binding{BindingKey: registration.BindingKey}
	for _, entity := range registration.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key:      entity.Key,
			EntityID: "ent_" + entity.Key,
		})
	}
	return binding, nil
}

func (session *fencingSession) ServeCommands(ctx context.Context, _ adapter.CommandHandler) error {
	close(session.entered)
	select {
	case <-session.fence:
		return adapter.ErrRuntimeFenced
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (session *fencingSession) Close() error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.closed = true
	return nil
}

func (session *fencingSession) SetHealth(context.Context, adapter.HealthReport) error {
	return nil
}

func (session *fencingSession) ReportEntityAvailability(
	context.Context,
	[]adapter.EntityAvailabilityReport,
) error {
	return nil
}

func (session *fencingSession) PublishObservation(
	_ context.Context,
	_ adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed {
		session.publishedAfterClose = true
	}
	return "obs_1", nil
}

func (session *fencingSession) PublishEntityEvent(
	_ context.Context,
	_ adapter.EntityEvent,
) (adapter.EntityEventID, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed {
		session.publishedAfterClose = true
	}
	return "evt_1", nil
}

// isClosed reports whether the Session closed.
func (session *fencingSession) isClosed() bool {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.closed
}

// publishedAfterSessionClose reports whether any publication reached the
// Session after it closed. It must stay false: publishers stop before the
// Session closes.
func (session *fencingSession) publishedAfterSessionClose() bool {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.publishedAfterClose
}

// tickingPowerDevice is one Entity with a multi-value output series, so
// StartScripts runs an Entity ticker until it is stopped.
func tickingPowerDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-light",
		Name:       "Simulated light",
		Kind:       "light",
		Entities: []scripted.EntitySpec{{
			Key:  "power",
			Name: "Power",
			Type: "hearth.power/v1",
			Support: map[string]any{
				"state":      map[string]any{},
				"operations": map[string]any{"set": map[string]any{}},
			},
			Initial: false,
			Outputs: &scripted.OutputsSpec{
				Interval: scripted.Duration(tickInterval),
				Values:   []any{true, false},
			},
		}},
	}
}

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

// waitForControlListening waits until the control channel answers on addr,
// proving the control worker is running before the Session fails.
func waitForControlListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(sessionFailureWait)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control channel never listened on %s", addr)
}

// runScriptedSessionUntilFenced runs one scripted process with a live caller
// context, waits for the control channel (when controlAddr is set) and Command
// serving to be active, then fails ServeCommands with a Session error. It
// bounds the wait after fencing so a shutdown that joins the worker loops on
// the caller's context fails the test instead of hanging it.
func runScriptedSessionUntilFenced(t *testing.T, controlAddr string) (*fencingSession, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := newFencingSession()
	config := Config{
		AdapterID:   "simulator",
		NATSURL:     "nats://127.0.0.1:4222",
		ControlAddr: controlAddr,
		Devices:     []scripted.DeviceSpec{tickingPowerDevice()},
	}
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runScriptedSession(ctx, config, session, slog.New(slog.DiscardHandler))
	}()
	if controlAddr != "" {
		waitForControlListening(t, controlAddr)
	}
	select {
	case <-session.entered:
	case <-time.After(sessionFailureWait):
		t.Fatal("runScriptedSession never reached ServeCommands")
	}
	if ctx.Err() != nil {
		t.Fatalf("caller context ended before fencing: %v", ctx.Err())
	}
	close(session.fence)
	select {
	case runErr := <-runErrors:
		return session, runErr
	case <-time.After(sessionFailureWait):
		t.Fatal("runScriptedSession did not return after the Session failed with a live caller context")
		return session, nil
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

// This test protects the ticker-only path of the same failure: with no control
// channel, StartScripts must still be stopped before the Session closes, so an
// active ticker cannot keep runScriptedSession from returning.
func TestRunScriptedSessionReturnsSessionErrorWithTickerOnly(t *testing.T) {
	t.Parallel()
	session, runErr := runScriptedSessionUntilFenced(t, "")
	if !errors.Is(runErr, adapter.ErrRuntimeFenced) {
		t.Fatalf("runScriptedSession error = %v, want %v", runErr, adapter.ErrRuntimeFenced)
	}
	if !session.isClosed() {
		t.Fatal("Session was not closed after shutdown")
	}
	if session.publishedAfterSessionClose() {
		t.Fatal("publication reached the Session after it closed")
	}
}
