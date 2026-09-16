package simulator_test

import (
	"context"
	"errors"
	"testing"

	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic Run error instead of
// propagating context cancellation for the executable wrapper to report as
// process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := simulatorapp.Run(ctx, scriptedConfig(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}
