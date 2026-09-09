package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
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
	}
	if err := Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}
