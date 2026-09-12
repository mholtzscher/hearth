package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

// TestCoreNATSConnectionBoundsSocketWrites protects the shared Core
// connection's policy: unlimited reconnects on the shared cadence, nats.go's
// default reconnect buffering, so a publication during reconnect is buffered
// and delivered afterwards exactly as before, and one bounded socket write
// deadline. One connection now carries every Core subscription and the Device
// Fact relay's publications; without the bound a stalled shared connection
// holds Conn.mu for nats.go's one-minute default, and both readiness and the
// relay's outbox read on that connection keep relay Drain past its five-second
// budget.
func TestCoreNATSConnectionBoundsSocketWrites(t *testing.T) {
	t.Parallel()
	server := startLifecycleNATSServer(t)
	connection, err := connectCoreNATS(t.Context(), server.ClientURL(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)

	if connection.Opts.FlusherTimeout != devicesnats.CoreNATSWriteTimeout {
		t.Fatalf(
			"shared connection FlusherTimeout = %s, want %s",
			connection.Opts.FlusherTimeout, devicesnats.CoreNATSWriteTimeout,
		)
	}
	if devicesnats.CoreNATSWriteTimeout >= 3*time.Second {
		t.Fatalf(
			"CoreNATSWriteTimeout = %s, want a bound far inside the five-second shutdown budget",
			devicesnats.CoreNATSWriteTimeout,
		)
	}
	if connection.Opts.ReconnectBufSize != natsgo.DefaultReconnectBufSize {
		t.Fatalf(
			"shared connection ReconnectBufSize = %d, want the default %d",
			connection.Opts.ReconnectBufSize, natsgo.DefaultReconnectBufSize,
		)
	}
	if connection.Opts.MaxReconnect != -1 {
		t.Fatalf("shared connection MaxReconnect = %d, want -1", connection.Opts.MaxReconnect)
	}
	if connection.Opts.ReconnectWait != natsReconnectWait {
		t.Fatalf(
			"shared connection ReconnectWait = %s, want %s",
			connection.Opts.ReconnectWait, natsReconnectWait,
		)
	}
}

// This test protects startup cancellation reporting and fails if a canceled
// operation context still dials NATS or reports a connection.
func TestCoreNATSConnectHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	logger, recorder := withRecording(slog.LevelDebug)
	natsLogger := logger.With("component", "nats")

	server := startLifecycleNATSServer(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connectCoreNATS(canceled, server.ClientURL(), natsLogger); err == nil {
		t.Fatal("connectCoreNATS succeeded with a canceled context")
	} else if stage := ErrorStage(err); stage != "connect_nats" {
		t.Fatalf("connect error stage = %q, want connect_nats (err: %v)", stage, err)
	}
	if connected := recordsWithEvent(recorder.snapshot(), "dependency.connected"); len(connected) != 0 {
		t.Fatalf("canceled connect emitted dependency.connected: %#v", connected)
	}
}

// This test protects safe async NATS failure reporting and fails if the
// error callback panics, drops the diagnostic, or leaks the raw subject or
// error text.
func TestCoreNATSAsyncErrorIsSafe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	logger, recorder := withRecording(slog.LevelDebug)
	server := startLifecycleNATSServer(t)
	connection, err := connectCoreNATS(ctx, server.ClientURL(), logger.With("component", "nats"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
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
