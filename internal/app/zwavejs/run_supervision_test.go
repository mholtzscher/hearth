package zwavejs //nolint:testpackage // Tests exercise package-private lifecycle supervision.

import (
	"context"
	"errors"
	"testing"
)

// TestSuperviseCancelsSiblingAndReturnsTerminalError preserves the first component's error when stopping its sibling.
func TestSuperviseCancelsSiblingAndReturnsTerminalError(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	terminalErr := errors.New("terminal adapter failure")

	err := supervise(
		context.Background(),
		func(context.Context) error {
			<-started
			return terminalErr
		},
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("supervise error = %v, want terminal failure", err)
	}
}
