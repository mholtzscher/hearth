package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"testing"
)

// This test protects clean startup cancellation and fails if cancellation is
// reported as the unrelated startup failure that happened while stopping.
func TestMapStartupCancellationReturnsContextCanceled(t *testing.T) {
	t.Parallel()
	startupErr := errors.New("connect to NATS: EOF")
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if got := mapStartupCancellation(cancelled, startupErr); !errors.Is(got, context.Canceled) {
		t.Fatalf("cancelled startup error = %v, want context.Canceled", got)
	}
}

// This test protects startup deadline diagnostics and fails if an expired
// deadline is incorrectly reported as a successful process stop.
func TestMapStartupCancellationPreservesDeadlineFailure(t *testing.T) {
	t.Parallel()
	startupErr := failStage("connect_nats", context.DeadlineExceeded)
	expired, stop := context.WithTimeout(context.Background(), 0)
	defer stop()
	if got := mapStartupCancellation(expired, startupErr); !errors.Is(got, startupErr) {
		t.Fatalf("expired startup error = %v, want %v", got, startupErr)
	}
}

// This test protects ordinary startup failure diagnostics while the operation
// context remains live.
func TestMapStartupCancellationPreservesLiveFailure(t *testing.T) {
	t.Parallel()
	startupErr := errors.New("connect to NATS: EOF")
	if got := mapStartupCancellation(context.Background(), startupErr); !errors.Is(got, startupErr) {
		t.Fatalf("live startup error = %v, want %v", got, startupErr)
	}
}
