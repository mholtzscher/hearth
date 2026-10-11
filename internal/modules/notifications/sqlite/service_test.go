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

// A6: concurrent sends are not capped, public drain permits reserved internal sends,
// and client cancellation cannot cancel an already admitted provider request.
func TestServiceConcurrentSendsCancellationAndSeparateDrainGates(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan sendResult, 6)
	for range 5 {
		in := input(t, d.ID)
		go func() { a, sendErr := s.SendPublic(ctx, in); results <- sendResult{a, sendErr} }()
	}
	for range 5 {
		select {
		case <-entered:
		case <-time.After(time.Second * 5):
			t.Fatal("concurrent sends not admitted")
		}
	}
	cancel()
	s.ClosePublicAdmission()
	if _, err = s.SendPublic(context.Background(), input(t, d.ID)); !errors.Is(err, notifications.ErrAdmissionClosed) {
		t.Fatalf("public gate: %v", err)
	}
	// The internal gate remains open for an Automation's already reserved link.
	internal := input(t, d.ID)
	go func() {
		a, sendErr := s.Send(context.Background(), internal)
		results <- sendResult{a, sendErr}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reserved internal send not admitted during public drain")
	}
	short, stop := context.WithCancel(context.Background())
	stop()
	if err = s.Drain(short); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain canceled wait: %v", err)
	}
	if _, err = s.Send(context.Background(), input(t, d.ID)); !errors.Is(err, notifications.ErrAdmissionClosed) {
		t.Fatalf("internal gate: %v", err)
	}
	close(release)
	for range 6 {
		result := receive(t, results)
		if result.err != nil || result.attempt.Outcome != notifications.Accepted {
			t.Fatalf("client cancellation affected send: %+v", result)
		}
	}
	if err = s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListAttempts(context.Background(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 6 || calls.Load() != 6 {
		t.Fatalf("durable attempts: %+v %v", page, err)
	}
}

//nolint:gocognit // Fault phases share the same durable recovery assertions.
func TestStorageFailuresLatchAdmissionAndRecoverWithoutReplay(t *testing.T) {
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
			if !errors.Is(err, notifications.ErrPersistence) || a.Outcome != "" || s.Ready() {
				t.Fatalf("persistence failure promoted evidence: %+v %v ready=%v", a, err, s.Ready())
			}
			wantCalls := int32(0)
			if phase == "terminal" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("dispatch calls %d want %d", calls.Load(), wantCalls)
			}
			if _, err = s.Send(
				context.Background(),
				input(t, d.ID),
			); !errors.Is(
				err,
				notifications.ErrAdmissionClosed,
			) {
				t.Fatalf("latch: %v", err)
			}
			if _, err = f.database.ExecContext(context.Background(), "DROP TRIGGER fail_attempt"); err != nil {
				t.Fatal(err)
			}
			if err = f.database.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := sqlite.NewRepository(dbtest.OpenMigrated(t, f.path))
			restarted, err := notifications.NewService(context.Background(), reopened, sender)
			if err != nil || !restarted.Ready() {
				t.Fatalf("restart: %v", err)
			}
			if calls.Load() != wantCalls {
				t.Fatal("recovery resent request")
			}
			if phase == "terminal" {
				saved, readErr := restarted.GetAttempt(context.Background(), in.AttemptID)
				if readErr != nil || saved.Outcome != notifications.Unknown || saved.ReasonCode != "core_restarted" ||
					saved.Receipt != "" {
					t.Fatalf("lost commit recovery: %+v %v", saved, readErr)
				}
			}
		})
	}
}

type observedPersistence struct {
	notifications.Repository

	ctxErr    error
	remaining time.Duration
}

func (r *observedPersistence) Complete(
	ctx context.Context,
	id notifications.AttemptID,
	e notifications.Evidence,
	at time.Time,
) (notifications.Attempt, error) {
	r.ctxErr = ctx.Err()
	deadline, _ := ctx.Deadline()
	r.remaining = time.Until(deadline)
	return r.Repository.Complete(ctx, id, e, at)
}
func TestDeadlinePreservesNonsubmissionAndIgnoresLateAcceptance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		evidence, want notifications.Evidence
	}{
		{
			name:     "late acceptance",
			evidence: notifications.Evidence{Outcome: notifications.Accepted, Receipt: "late"},
			want:     notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"},
		},
		{
			name:     "proven expiry before submission",
			evidence: notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"},
			want:     notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"},
		},
		{
			name:     "proven no responder",
			evidence: notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"},
			want:     notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"},
		},
		{
			name:     "invalid nonsubmission evidence",
			evidence: notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired", Receipt: "invalid"},
			want:     notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testDeadlineEvidence(t, tc.evidence, tc.want)
		})
	}
}

func testDeadlineEvidence(t *testing.T, evidence, want notifications.Evidence) {
	t.Helper()
	f := newFixture(t)
	d := register(t, f, f.registration)
	repo := &observedPersistence{Repository: f.repo}
	s, err := notifications.NewService(
		context.Background(),
		repo,
		senderFunc(func(ctx context.Context, _ notifications.Attempt) notifications.Evidence {
			<-ctx.Done()
			return evidence
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Send(context.Background(), input(t, d.ID))
	if err != nil || a.Outcome != want.Outcome || a.ReasonCode != want.ReasonCode || a.Receipt != want.Receipt {
		t.Fatalf("deadline evidence: %+v %v; want %+v", a, err, want)
	}
	if repo.ctxErr != nil || repo.remaining < 4*time.Second || repo.remaining > notifications.PersistenceTimeout {
		t.Fatalf("commit context err=%v remaining=%s", repo.ctxErr, repo.remaining)
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
