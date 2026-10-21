package notifications_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestServiceConcurrentSendsAreNotCapped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{}, 5)
		release := make(chan struct{})
		s := newPolicyService(
			t,
			&policyRepository{},
			func(context.Context, notifications.Attempt) notifications.Evidence {
				entered <- struct{}{}
				<-release
				return notifications.Evidence{Outcome: notifications.Accepted}
			},
		)
		results := make(chan policyResult, 5)
		for range 5 {
			in := policyInput(t)
			go func() { a, err := s.SendPublic(t.Context(), in); results <- policyResult{a, err} }()
		}
		for range 5 {
			<-entered // Every provider must enter before any provider can finish.
		}
		close(release)
		for range 5 {
			assertPolicyAccepted(t, <-results)
		}
		if err := s.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestServiceCallerCancellationDoesNotCancelAdmittedSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan context.Context, 1)
		release := make(chan struct{})
		repo := &policyRepository{
			complete: func(ctx context.Context, id notifications.AttemptID, e notifications.Evidence, _ time.Time) (notifications.Attempt, error) {
				if ctx.Err() != nil {
					t.Errorf("caller canceled persistence: %v", ctx.Err())
				}
				return notifications.Attempt{ID: id, Outcome: e.Outcome}, nil
			},
		}
		s := newPolicyService(t, repo, func(ctx context.Context, _ notifications.Attempt) notifications.Evidence {
			entered <- ctx
			select {
			case <-release:
				return notifications.Evidence{Outcome: notifications.Accepted}
			case <-ctx.Done():
				return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		results := make(chan policyResult, 1)
		in := policyInput(t)
		go func() { a, err := s.SendPublic(ctx, in); results <- policyResult{a, err} }()
		owned := <-entered
		cancel()
		if err := s.Drain(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("drain canceled wait: %v", err)
		}
		if owned.Err() != nil {
			t.Fatalf("caller or canceled drain canceled provider: %v", owned.Err())
		}
		close(release)
		assertPolicyAccepted(t, <-results)
	})
}

func TestServicePublicDrainLeavesReservedInternalAdmissionOpen(t *testing.T) {
	t.Parallel()
	s := newPolicyService(t, &policyRepository{}, func(context.Context, notifications.Attempt) notifications.Evidence {
		return notifications.Evidence{Outcome: notifications.Accepted}
	})
	s.ClosePublicAdmission()
	if _, err := s.SendPublic(t.Context(), policyInput(t)); !errors.Is(err, notifications.ErrAdmissionClosed) {
		t.Fatalf("public gate: %v", err)
	}
	a, err := s.Send(t.Context(), policyInput(t))
	assertPolicyAccepted(t, policyResult{a, err})
	if err = s.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Send(t.Context(), policyInput(t)); !errors.Is(err, notifications.ErrAdmissionClosed) {
		t.Fatalf("internal gate: %v", err)
	}
}

func TestServiceCanceledDrainStillJoinsTerminalPersistence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		repo := &policyRepository{
			complete: func(ctx context.Context, id notifications.AttemptID, e notifications.Evidence, _ time.Time) (notifications.Attempt, error) {
				close(entered)
				<-release
				if ctx.Err() != nil {
					t.Errorf("drain canceled persistence: %v", ctx.Err())
				}
				return notifications.Attempt{ID: id, Outcome: e.Outcome}, nil
			},
		}
		s := newPolicyService(t, repo, func(context.Context, notifications.Attempt) notifications.Evidence {
			return notifications.Evidence{Outcome: notifications.Accepted}
		})
		results := make(chan policyResult, 1)
		in := policyInput(t)
		go func() { a, err := s.Send(t.Context(), in); results <- policyResult{a, err} }()
		<-entered
		short, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.Drain(short); !errors.Is(err, context.Canceled) {
			t.Fatalf("drain canceled wait: %v", err)
		}
		if _, err := s.Send(t.Context(), policyInput(t)); !errors.Is(err, notifications.ErrAdmissionClosed) {
			t.Fatalf("canceled drain did not close admission: %v", err)
		}
		drained := make(chan error, 1)
		go func() { drained <- s.Drain(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-drained:
			t.Fatalf("drain skipped terminal persistence: %v", err)
		default:
		}
		close(release)
		assertPolicyAccepted(t, <-results)
		if err := <-drained; err != nil {
			t.Fatal(err)
		}
	})
}

func TestServiceInvalidOrCanceledSendNeverAdmits(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"blank message", "canceled caller"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := &policyRepository{
				admit: func(context.Context, notifications.SendInput, time.Time, time.Time) (notifications.Attempt, bool, error) {
					t.Error("rejected input reached repository")
					return notifications.Attempt{}, false, nil
				},
			}
			s := newPolicyService(t, repo, func(context.Context, notifications.Attempt) notifications.Evidence {
				t.Error("rejected input reached provider")
				return notifications.Evidence{}
			})
			in := policyInput(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := notifications.ErrInvalidInput
			if name == "blank message" {
				in.Message.Text = "  "
			} else {
				cancel()
				want = context.Canceled
			}
			if _, err := s.SendPublic(ctx, in); !errors.Is(err, want) {
				t.Fatalf("rejected send: %v, want %v", err, want)
			}
		})
	}
}
