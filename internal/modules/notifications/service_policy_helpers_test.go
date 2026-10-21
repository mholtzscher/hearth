package notifications_test

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// policyRepository scripts only the operations used by Send; it does not model storage.
// Embedded, unused methods fail immediately if a policy test crosses another seam.
type policyRepository struct {
	notifications.Repository

	admit    func(context.Context, notifications.SendInput, time.Time, time.Time) (notifications.Attempt, bool, error)
	complete func(context.Context, notifications.AttemptID, notifications.Evidence, time.Time) (notifications.Attempt, error)
}

func (*policyRepository) Recover(context.Context, time.Time) error { return nil }

func (r *policyRepository) Admit(
	ctx context.Context,
	in notifications.SendInput,
	start, deadline time.Time,
) (notifications.Attempt, bool, error) {
	if r.admit != nil {
		return r.admit(ctx, in, start, deadline)
	}
	return notifications.Attempt{ID: in.AttemptID, DestinationID: in.DestinationID,
		Message: in.Message, StartedAt: start, Deadline: deadline}, true, nil
}

func (r *policyRepository) Complete(
	ctx context.Context,
	id notifications.AttemptID,
	e notifications.Evidence,
	at time.Time,
) (notifications.Attempt, error) {
	if r.complete != nil {
		return r.complete(ctx, id, e, at)
	}
	return notifications.Attempt{
		ID:          id,
		Outcome:     e.Outcome,
		ReasonCode:  e.ReasonCode,
		Receipt:     e.Receipt,
		CompletedAt: &at,
	}, nil
}

type policySender func(context.Context, notifications.Attempt) notifications.Evidence

func (fn policySender) Send(ctx context.Context, a notifications.Attempt) notifications.Evidence {
	return fn(ctx, a)
}

func newPolicyService(t *testing.T, repo *policyRepository, sender policySender) *notifications.Service {
	t.Helper()
	s, err := notifications.NewService(t.Context(), repo, sender)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func policyInput(t *testing.T) notifications.SendInput {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	return notifications.SendInput{
		AttemptID:     id,
		DestinationID: destination,
		Message:       notifications.Message{Text: "alert"},
	}
}

type policyResult struct {
	attempt notifications.Attempt
	err     error
}

func assertPolicyAccepted(t *testing.T, result policyResult) {
	t.Helper()
	if result.err != nil || result.attempt.Outcome != notifications.Accepted {
		t.Fatalf("admitted send: %+v", result)
	}
}
