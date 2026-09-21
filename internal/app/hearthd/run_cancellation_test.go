package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic staged Run error instead of
// propagating context cancellation for the executable wrapper to report as
// process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:4222", NATSURL: "nats://127.0.0.1:4222",
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		Agent:      requiredAgentConfig(t),
	}
	if err := Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}

// This test protects the bounded shutdown contract and fails if a client that
// connected without completing a request turns a clean cancellation into a
// staged failure or delays teardown past the five-second HTTP window. net/http
// cannot reclaim such a socket inside that window: it is indistinguishable from
// a slow client, and freeing it needs ReadHeaderTimeout plus a shutdown poll
// interval. The connection also stays open, so a bounded window must force it
// closed instead of waiting longer.
func TestRunCancellationForceClosesUnfinishedClientConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	client := newNonPoolingHTTPClient(t)
	address := unusedLoopbackAddress(t)
	config := Config{HouseholdTimezone: "UTC",
		HTTPAddr: address, NATSURL: server.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		Agent:      requiredAgentConfig(t),
	}

	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, config, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHTTPStatus(ctx, t, client, address, "/healthz", runErrors)
	waitForCoreHTTPStatus(ctx, t, client, address, "/readyz", runErrors)

	// The unfinished connection queues first, so a completed request on a new
	// connection proves the server accepted it: the accept queue is FIFO.
	unfinished, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer unfinished.Close()
	waitForCoreHTTPStatus(ctx, t, client, address, "/healthz", runErrors)

	started := time.Now()
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run after cancellation = %v, want nil", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	if elapsed := time.Since(started); elapsed > shutdownTimeout+2*time.Second {
		t.Fatalf("cancellation took %v, want within the %v HTTP shutdown window", elapsed, shutdownTimeout)
	}
	unfinished.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, readErr := unfinished.Read(make([]byte, 1)); readErr == nil || errors.Is(readErr, os.ErrDeadlineExceeded) {
		t.Fatalf("cancellation left the unfinished client connection open (read err: %v)", readErr)
	}

	// The forced close still releases the address, database, and NATS
	// connection, so the same configuration starts again and stops cleanly.
	secondContext, stopSecondCore := context.WithCancel(ctx)
	defer stopSecondCore()
	secondErrors := make(chan error, 1)
	go func() {
		secondErrors <- Run(secondContext, config, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHTTPStatus(ctx, t, client, address, "/healthz", secondErrors)
	waitForCoreHTTPStatus(ctx, t, client, address, "/readyz", secondErrors)
	stopSecondCore()
	select {
	case runErr := <-secondErrors:
		if runErr != nil {
			t.Fatalf("second Run after cancellation = %v, want nil", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second Core did not stop")
	}
}
