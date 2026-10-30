package notifications_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestCompletionConflictDoesNotAdoptOrResend(t *testing.T) {
	t.Parallel()
	calls, completions := 0, 0
	repo := &policyRepository{
		complete: func(context.Context, notifications.AttemptID, notifications.Evidence, time.Time) (notifications.Attempt, error) {
			completions++
			return notifications.Attempt{}, notifications.ErrCompletionConflict
		},
	}
	service := newPolicyService(t, repo, func(context.Context, notifications.Attempt) notifications.Evidence {
		calls++
		return notifications.Evidence{Outcome: notifications.Accepted, Receipt: "private receipt"}
	})
	got, err := service.Send(t.Context(), policyInput(t))
	if !errors.Is(err, notifications.ErrCompletionConflict) || !errors.Is(err, notifications.ErrPersistence) ||
		got.ID != "" ||
		got.Outcome != "" {
		t.Fatalf("completion conflict lost or adopted: %+v %v", got, err)
	}
	if calls != 1 || completions != 1 {
		t.Fatalf("dispatch=%d completions=%d", calls, completions)
	}
}
