package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

// This test protects expected NATS teardown and fails if closing the core
// connection during teardown ever reports an Error.
func TestCoreNATSExpectedCloseStaysDebug(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelDebug)
	natsLogger := logger.With("component", "nats")

	server := startLifecycleNATSServer(t)
	closing := &atomic.Bool{}
	connection, err := connectCoreNATS(ctx, server.ClientURL(), natsLogger, closing)
	if err != nil {
		t.Fatal(err)
	}
	connected := waitForRecord(t, recorder, "dependency.connected", 10*time.Second)
	requireRecordAttr(t, connected, "component", "nats")
	requireRecordAttr(t, connected, "dependency", "nats")

	closing.Store(true)
	connection.Close()
	closed := waitForRecord(t, recorder, "dependency.closed", 10*time.Second)
	if closed.Level != slog.LevelDebug {
		t.Fatalf("expected dependency.closed level = %v, want Debug", closed.Level)
	}
	requireRecordAttr(t, closed, "component", "nats")
	requireRecordAttr(t, closed, "dependency", "nats")
	if failures := errorRecords(recorder.snapshot()); len(failures) != 0 {
		t.Fatalf("expected NATS close emitted Error records: %#v", failures)
	}
	if lost := recordsWithEvent(recorder.snapshot(), "dependency.disconnected"); len(lost) != 0 {
		t.Fatalf("expected teardown reported connection loss: %#v", lost)
	}
}

// This test protects unexpected terminal-close diagnosis and fails if a close
// outside teardown is silent or loses the unexpected_close reason.
func TestCoreNATSUnexpectedCloseIsError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelDebug)
	natsLogger := logger.With("component", "nats")

	server := startLifecycleNATSServer(t)
	closing := &atomic.Bool{}
	connection, err := connectCoreNATS(ctx, server.ClientURL(), natsLogger, closing)
	if err != nil {
		t.Fatal(err)
	}
	waitForRecord(t, recorder, "dependency.connected", 10*time.Second)

	// Close without marking teardown: the ClosedHandler must diagnose this as
	// an unexpected terminal close even though Run supervision stays alive.
	connection.Close()
	closed := waitForRecord(t, recorder, "dependency.closed", 10*time.Second)
	if closed.Level != slog.LevelError {
		t.Fatalf("unexpected dependency.closed level = %v, want Error", closed.Level)
	}
	requireRecordAttr(t, closed, "component", "nats")
	requireRecordAttr(t, closed, "dependency", "nats")
	requireRecordAttr(t, closed, "reason_code", "unexpected_close")
}

// This test protects the disconnect/reconnect evidence ordering and fails if
// connection loss is silent, reconnects claim restored Adapter health, or
// recovery never reports the reconnect.
func TestCoreNATSDisconnectAndReconnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelDebug)
	natsLogger := logger.With("component", "nats")

	address := freeLoopbackAddr(t)
	server := startFixedPortNATSServer(t, address)
	closing := &atomic.Bool{}
	connection, err := connectCoreNATS(ctx, "nats://"+address, natsLogger, closing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closing.Store(true)
		connection.Close()
	})
	waitForRecord(t, recorder, "dependency.connected", 10*time.Second)

	server.Shutdown()
	server.WaitForShutdown()
	disconnected := waitForRecord(t, recorder, "dependency.disconnected", 10*time.Second)
	if disconnected.Level != slog.LevelWarn {
		t.Fatalf("dependency.disconnected level = %v, want Warn", disconnected.Level)
	}
	requireRecordAttr(t, disconnected, "component", "nats")
	requireRecordAttr(t, disconnected, "dependency", "nats")
	requireRecordAttr(t, disconnected, "error_code", "nats_disconnected")

	startFixedPortNATSServer(t, address)
	reconnected := waitForRecord(t, recorder, "dependency.reconnected", 15*time.Second)
	if reconnected.Level != slog.LevelInfo {
		t.Fatalf("dependency.reconnected level = %v, want Info", reconnected.Level)
	}
	requireRecordAttr(t, reconnected, "component", "nats")
	requireRecordAttr(t, reconnected, "dependency", "nats")
}

// This test protects lifecycle-context classification and fails if a canceled
// operation context still dials NATS or reports a connection.
func TestCoreNATSConnectHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	logger, recorder := withRecording(slog.LevelDebug)
	natsLogger := logger.With("component", "nats")

	server := startLifecycleNATSServer(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connectCoreNATS(canceled, server.ClientURL(), natsLogger, &atomic.Bool{}); err == nil {
		t.Fatal("connectCoreNATS succeeded with a canceled context")
	} else if stage := ErrorStage(err); stage != "connect_nats" {
		t.Fatalf("connect error stage = %q, want connect_nats (err: %v)", stage, err)
	}
	if connected := recordsWithEvent(recorder.snapshot(), "dependency.connected"); len(connected) != 0 {
		t.Fatalf("canceled connect emitted dependency.connected: %#v", connected)
	}
}

func TestCoreNATSAsyncErrorIsSafe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	logger, recorder := withRecording(slog.LevelDebug)
	server := startLifecycleNATSServer(t)
	closing := &atomic.Bool{}
	connection, err := connectCoreNATS(ctx, server.ClientURL(), logger.With("component", "nats"), closing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closing.Store(true)
		connection.Close()
	})
	const sentinel = "secret-subject-and-error"
	connection.Opts.AsyncErrorCB(connection, &natsgo.Subscription{Subject: sentinel}, errors.New(sentinel))
	record := waitForRecord(t, recorder, "dependency.operation_failed", time.Second)
	requireRecordAttr(t, record, "error_code", "nats_async_error")
	requireRecordAttr(t, record, "component", "nats")
	if record.Level != slog.LevelError {
		t.Fatalf("async failure level = %v, want Error", record.Level)
	}
	if strings.Contains(fmt.Sprint(recorder.snapshot()), sentinel) {
		t.Fatal("async failure exposed raw subject or error")
	}
}

func startFixedPortNATSServer(t *testing.T, address string) *natsserver.Server {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: host, Port: port, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}
