package hearthd //nolint:testpackage // Tests error-exit shutdown order through package-private assembly.

import (
	"context"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// withShutdownOrderProbe records the process shutdown order for one Run. It is
// a test-only RunOption: production assembly passes no options and leaves the
// probe nil, so parallel lifecycle tests without a probe record nothing.
func withShutdownOrderProbe(probe func(step string)) RunOption {
	return func(options *runOptions) {
		options.shutdownOrderProbe = probe
	}
}

// C3/B8 regression: an HTTP bind failure must exit through the single shutdown
// order that closes automation and direct admission and joins the scheduler
// before stopping health supervision and canceling shared dependencies. The
// occupied port deterministically triggers the http_listen error exit after
// the scheduler started; the recorded step order proves health supervision
// stopped only after admitted execution drained, so the scheduler can never
// keep admitting new work while dependencies tear down. With the previous
// defer order the supervisor stopped first and the recorded order differed.
func TestRunHTTPListenFailureKeepsShutdownOrder(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	natsServer := startLifecycleNATSServer(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	var probeMutex sync.Mutex
	var shutdownOrder []string
	runErr := Run(ctx, Config{
		HouseholdTimezone: "UTC",
		HTTPAddr:          occupied.Addr().String(),
		NATSURL:           natsServer.ClientURL(),
		SQLitePath:        filepath.Join(t.TempDir(), "hearth.db"),
	}, slog.New(slog.DiscardHandler),
		WithSchedulerWakeup(make(chan struct{})),
		withShutdownOrderProbe(func(step string) {
			probeMutex.Lock()
			defer probeMutex.Unlock()
			shutdownOrder = append(shutdownOrder, step)
		}),
	)
	if runErr == nil {
		t.Fatal("Run succeeded while the HTTP port was occupied")
	}
	if stage := ErrorStage(runErr); stage != "http_listen" {
		t.Fatalf("error stage = %q, want http_listen", stage)
	}
	probeMutex.Lock()
	defer probeMutex.Unlock()
	wantShutdownOrder := []string{"execution drained", "health supervision stopped", "dependencies canceled"}
	if !slices.Equal(shutdownOrder, wantShutdownOrder) {
		t.Fatalf("shutdown order = %q, want %q", shutdownOrder, wantShutdownOrder)
	}
}
