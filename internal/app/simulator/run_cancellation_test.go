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
	config := simulatorapp.Config{
		AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222",
		BindingKey: "simulated-light", Scenario: "happy",
	}
	if err := simulatorapp.Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}
