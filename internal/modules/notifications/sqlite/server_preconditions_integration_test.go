//go:build integration

package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestCoreMetadataPreconditionsAreInternal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	in := input(t, destination)
	at := time.Now().UTC()
	evidence := notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"}
	ctx := t.Context()
	for name, check := range map[string]func() error{
		"start time":       func() error { _, _, e := f.repo.Admit(ctx, in, time.Time{}, at); return e },
		"deadline":         func() error { _, _, e := f.repo.Admit(ctx, in, at, at); return e },
		"completion time":  func() error { _, e := f.repo.Complete(ctx, in.AttemptID, evidence, time.Time{}); return e },
		"recovery time":    func() error { return f.repo.Recover(ctx, time.Time{}) },
		"retention cutoff": func() error { _, e := f.repo.DeleteHistoryBefore(ctx, time.Time{}, 1); return e },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failure := check()
			if failure == nil || errors.Is(failure, notifications.ErrInvalidInput) ||
				errors.Is(failure, notifications.ErrDestinationNotFound) ||
				errors.Is(failure, notifications.ErrAttemptNotFound) {
				t.Fatalf("Core metadata error=%v, want internal precondition failure", failure)
			}
		})
	}
}

func TestCoreMetadataChecksPreserveInvalidInputRejection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	destination, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	in := input(t, destination)
	in.Message.Text = ""
	if _, _, err = f.repo.Admit(
		t.Context(),
		in,
		time.Time{},
		time.Time{},
	); !errors.Is(
		err,
		notifications.ErrInvalidInput,
	) {
		t.Fatalf("malformed send lost input classification: %v", err)
	}
	if _, err = f.repo.Complete(
		t.Context(),
		"bad-id",
		notifications.Evidence{},
		time.Time{},
	); !errors.Is(
		err,
		notifications.ErrInvalidInput,
	) {
		t.Fatalf("malformed completion lost input classification: %v", err)
	}
	if _, err = f.repo.DeleteHistoryBefore(t.Context(), time.Now(), 0); !errors.Is(err, notifications.ErrInvalidInput) {
		t.Fatalf("invalid limit lost input classification: %v", err)
	}
}
