package agent //nolint:testpackage // Tests protect the turn owner's cancellation decision.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestExpectedTurnCancellationRequiresOwnerAndSingleCause(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		owner string
		cause error
		want  bool
	}{
		{"caller", "canceled", context.Canceled, true},
		{"wrapped caller", "canceled", fmt.Errorf("model: %w", context.Canceled), true},
		{"caller deadline", "deadline", context.DeadlineExceeded, true},
		{"dependency cancellation", "active", context.Canceled, false},
		{"dependency deadline", "active", context.DeadlineExceeded, false},
		{"different deadline", "canceled", context.DeadlineExceeded, false},
		{"unrelated fault", "canceled", errors.New("storage failed"), false},
		{"joined fault", "canceled", errors.Join(context.Canceled, errors.New("storage failed")), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			switch test.owner {
			case "canceled":
				owned, cancel := context.WithCancel(ctx)
				cancel()
				ctx = owned
			case "deadline":
				owned, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				ctx = owned
			}
			if got := expectedTurnCancellation(ctx, test.cause); got != test.want {
				t.Fatalf("expected cancellation = %t, want %t", got, test.want)
			}
		})
	}
}
