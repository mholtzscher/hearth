package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"testing"
)

// This test protects the startup cancellation contract behind Run and fails if
// a cancelled startup context still surfaces its startup error.
func TestMapStartupCancellationToNil(t *testing.T) {
	t.Parallel()
	startupErr := errors.New("connect to NATS: EOF")
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if got := mapStartupCancellationToNil(cancelled, startupErr); got != nil {
		t.Fatalf("cancelled startup error = %v, want nil", got)
	}
	if got := mapStartupCancellationToNil(context.Background(), startupErr); !errors.Is(got, startupErr) {
		t.Fatalf("live startup error = %v, want %v", got, startupErr)
	}
}
