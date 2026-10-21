package notifications_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestDeadlinePreservesNonsubmissionAndIgnoresLateAcceptance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		evidence, want notifications.Evidence
	}{
		{"late acceptance", notifications.Evidence{Outcome: notifications.Accepted, Receipt: "late"}, notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}},
		{"proven expiry before submission", notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"}, notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"}},
		{"proven no responder", notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"}, notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"}},
		{"invalid nonsubmission evidence", notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired", Receipt: "invalid"}, notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testPolicyDeadlineEvidence(t, tc.evidence, tc.want)
		})
	}
}

func testPolicyDeadlineEvidence(t *testing.T, evidence, want notifications.Evidence) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		var committed notifications.Evidence
		var ctxErr error
		var remaining time.Duration
		var completed bool
		repo := &policyRepository{
			complete: func(ctx context.Context, id notifications.AttemptID, e notifications.Evidence, at time.Time) (notifications.Attempt, error) {
				completed = true
				committed, ctxErr = e, ctx.Err()
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("persistence has no deadline")
				}
				remaining = time.Until(deadline)
				return notifications.Attempt{
					ID:          id,
					Outcome:     e.Outcome,
					ReasonCode:  e.ReasonCode,
					Receipt:     e.Receipt,
					CompletedAt: &at,
				}, nil
			},
		}
		s := newPolicyService(
			t,
			repo,
			func(ctx context.Context, a notifications.Attempt) notifications.Evidence {
				deadline, ok := ctx.Deadline()
				if !ok || !deadline.Equal(a.Deadline) ||
					a.Deadline.Sub(a.StartedAt) != notifications.SendTimeout {
					t.Errorf("provider deadline: %v, attempt: %+v", deadline, a)
				}
				<-ctx.Done()
				return evidence
			},
		)
		a, err := s.Send(t.Context(), policyInput(t))
		if err != nil || a.Outcome != want.Outcome || a.ReasonCode != want.ReasonCode ||
			a.Receipt != want.Receipt {
			t.Fatalf("deadline evidence: %+v %v; want %+v", a, err, want)
		}
		if !completed || committed != want {
			t.Fatalf("persisted evidence: %+v, completed=%v; want %+v", committed, completed, want)
		}
		if ctxErr != nil || remaining < 4*time.Second || remaining > notifications.PersistenceTimeout {
			t.Fatalf("commit context err=%v remaining=%s", ctxErr, remaining)
		}
	})
}
