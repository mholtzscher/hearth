//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

type senderFunc func(context.Context, notifications.Attempt) notifications.Evidence

func (fn senderFunc) Send(ctx context.Context, a notifications.Attempt) notifications.Evidence {
	return fn(ctx, a)
}

type sendResult struct {
	attempt notifications.Attempt
	err     error
}

func receive(t *testing.T, ch <-chan sendResult) sendResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(15 * time.Second):
		t.Fatal("send did not finish")
		return sendResult{}
	}
}

// Concurrent invocations must each commit independent evidence in real SQLite.
// Admission, cancellation and drain policy are tested by the module owner.
func TestServiceConcurrentSendsPersistSixDurableAttempts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	d := register(t, f, f.registration)
	entered := make(chan struct{}, 6)
	release := make(chan struct{})
	var calls atomic.Int32
	sender := senderFunc(func(ctx context.Context, _ notifications.Attempt) notifications.Evidence {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return notifications.Evidence{Outcome: notifications.Accepted}
		case <-ctx.Done():
			return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}
		}
	})
	s, err := notifications.NewService(context.Background(), f.repo, sender)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan sendResult, 6)
	ids := make(map[notifications.AttemptID]bool, 6)
	for range 6 {
		in := input(t, d.ID)
		ids[in.AttemptID] = true
		go func() { a, sendErr := s.SendPublic(t.Context(), in); results <- sendResult{a, sendErr} }()
	}
	for range 6 {
		select {
		case <-entered:
		case <-time.After(time.Second * 5):
			t.Fatal("concurrent sends not admitted")
		}
	}
	close(release)
	for range 6 {
		result := receive(t, results)
		if result.err != nil || result.attempt.Outcome != notifications.Accepted {
			t.Fatalf("concurrent send: %+v", result)
		}
	}
	if err = s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSixDurableAttempts(t, f, s, ids, calls.Load())
}

func assertSixDurableAttempts(
	t *testing.T,
	f fixture,
	s *notifications.Service,
	ids map[notifications.AttemptID]bool,
	calls int32,
) {
	t.Helper()
	page, err := s.ListAttempts(context.Background(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 6 || calls != 6 {
		t.Fatalf("durable attempts: %+v %v", page, err)
	}
	if err = f.database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := sqlite.NewRepository(dbtest.OpenMigrated(t, f.path))
	assertReopenedAttempts(t, reopened, page.Items, ids)
}

func assertReopenedAttempts(
	t *testing.T,
	repo notifications.Repository,
	attempts []notifications.Attempt,
	ids map[notifications.AttemptID]bool,
) {
	t.Helper()
	for _, a := range attempts {
		if !ids[a.ID] {
			t.Fatalf("unexpected or duplicate durable identity: %s", a.ID)
		}
		delete(ids, a.ID)
		saved, readErr := repo.GetAttempt(t.Context(), a.ID)
		if readErr != nil || saved.Outcome != notifications.Accepted || saved.CompletedAt == nil {
			t.Fatalf("reopened concurrent evidence: %+v %v", saved, readErr)
		}
	}
}

// The former API matrix also established that a missing durable destination
// cannot create history or invoke the provider. Keep that storage check real.
func TestServiceMissingDestinationNeverDispatchesOrCreatesHistory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var calls atomic.Int32
	s, err := notifications.NewService(
		t.Context(),
		f.repo,
		senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
			calls.Add(1)
			return notifications.Evidence{Outcome: notifications.Accepted}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendPublic(t.Context(), input(t, unknown)); !errors.Is(err, notifications.ErrDestinationNotFound) {
		t.Fatalf("missing destination: %v", err)
	}
	page, err := s.ListAttempts(t.Context(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 0 || calls.Load() != 0 {
		t.Fatalf("missing destination created evidence: %+v %v calls=%d", page, err, calls.Load())
	}
}

//nolint:gocognit // Fault phases share the same durable recovery assertions.
func TestStorageFailuresScopeToAttemptAndRecoverWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"initial", "terminal"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			d := register(t, f, f.registration)
			var calls atomic.Int32
			sender := senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
				calls.Add(1)
				return notifications.Evidence{Outcome: notifications.Accepted, Receipt: "external"}
			})
			s, err := notifications.NewService(context.Background(), f.repo, sender)
			if err != nil {
				t.Fatal(err)
			}
			statement := "CREATE TRIGGER fail_attempt BEFORE INSERT ON notification_attempts BEGIN SELECT RAISE(ABORT, 'storage fault'); END"
			if phase == "terminal" {
				statement = "CREATE TRIGGER fail_attempt BEFORE UPDATE ON notification_attempts BEGIN SELECT RAISE(ABORT, 'storage fault'); END"
			}
			if _, err = f.database.ExecContext(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
			in := input(t, d.ID)
			a, err := s.Send(context.Background(), in)
			if !errors.Is(err, notifications.ErrPersistence) || a.ID != "" || a.Outcome != "" {
				t.Fatalf("persistence failure promoted evidence: %+v %v", a, err)
			}
			wantCalls := int32(0)
			if phase == "terminal" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("dispatch calls %d want %d", calls.Load(), wantCalls)
			}
			if _, err = f.database.ExecContext(context.Background(), "DROP TRIGGER fail_attempt"); err != nil {
				t.Fatal(err)
			}
			next := input(t, d.ID)
			accepted, err := s.Send(context.Background(), next)
			if err != nil || accepted.ID != next.AttemptID || accepted.Outcome != notifications.Accepted ||
				accepted.Receipt != "external" || calls.Load() != wantCalls+1 {
				t.Fatalf("independent send after transient failure: %+v %v calls=%d", accepted, err, calls.Load())
			}
			assertFailedAttemptEvidence(t, s, in.AttemptID, phase)
			if err = f.database.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := sqlite.NewRepository(dbtest.OpenMigrated(t, f.path))
			restarted, err := notifications.NewService(context.Background(), reopened, sender)
			if err != nil {
				t.Fatalf("restart: %v", err)
			}
			if calls.Load() != wantCalls+1 {
				t.Fatal("recovery resent request")
			}
			retained, readErr := restarted.GetAttempt(context.Background(), next.AttemptID)
			if readErr != nil || retained.Outcome != notifications.Accepted || retained.Receipt != "external" {
				t.Fatalf("recovery revised committed outcome: %+v %v", retained, readErr)
			}
			if phase == "terminal" {
				saved, recoveryErr := restarted.GetAttempt(context.Background(), in.AttemptID)
				if recoveryErr != nil || saved.Outcome != notifications.Unknown ||
					saved.ReasonCode != "core_restarted" ||
					saved.Receipt != "" {
					t.Fatalf("lost commit recovery: %+v %v", saved, recoveryErr)
				}
			}
		})
	}
}

func assertFailedAttemptEvidence(t *testing.T, s *notifications.Service, id notifications.AttemptID, phase string) {
	t.Helper()
	prior, err := s.GetAttempt(context.Background(), id)
	if phase == "initial" {
		if !errors.Is(err, notifications.ErrAttemptNotFound) {
			t.Fatalf("failed admission invented history: %+v %v", prior, err)
		}
	} else if err != nil || prior.Outcome != "" || prior.CompletedAt != nil || prior.Receipt != "" {
		t.Fatalf("later send revised uncertain evidence: %+v %v", prior, err)
	}
}

func TestDistinctInvocationsAndIdentityCollisionNeverReplay(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	d := register(t, f, f.registration)
	var calls atomic.Int32
	s, err := notifications.NewService(
		context.Background(),
		f.repo,
		senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
			calls.Add(1)
			return notifications.Evidence{Outcome: notifications.Rejected, ReasonCode: "provider_rejected"}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	first := input(t, d.ID)
	a, err := s.Send(context.Background(), first)
	if err != nil || a.Outcome != notifications.Rejected {
		t.Fatalf("first: %+v %v", a, err)
	}
	b, err := s.Send(context.Background(), input(t, d.ID))
	if err != nil || b.ID == a.ID || calls.Load() != 2 {
		t.Fatalf("second: %+v %v calls=%d", b, err, calls.Load())
	}
	if _, err = s.Send(
		context.Background(),
		first,
	); !errors.Is(err, notifications.ErrPersistence) ||
		calls.Load() != 2 {
		t.Fatalf("collision replayed: %v calls=%d", err, calls.Load())
	}
}
