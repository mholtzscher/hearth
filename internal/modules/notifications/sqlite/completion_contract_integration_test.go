//go:build integration

package sqlite_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

func TestCompletionDistinguishesExactDuplicatesFromConflicts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "first", "identical duplicate", "conflicting duplicate", "changed receipt", "changed reason", "earlier completion", "later completion", "same instant different zone", "after recovery"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			exerciseCompletionContract(t, mode)
		})
	}
}

func exerciseCompletionContract(t *testing.T, mode string) {
	t.Helper()
	f := newFixture(t)
	destination := register(t, f, f.registration)
	in := input(t, destination.ID)
	start := time.Now().UTC()
	at := start.Add(time.Second)
	proposed := notifications.Evidence{Outcome: notifications.Accepted, Receipt: "first receipt"}
	if mode == "changed reason" {
		proposed = notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "transport_error"}
	}
	want := prepareCompletionContract(t, f, in, proposed, start, at, mode)
	switch mode {
	case "conflicting duplicate":
		proposed = notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}
		at = at.Add(time.Second)
	case "changed receipt":
		proposed.Receipt = "different receipt"
	case "changed reason":
		proposed = notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "provider_error"}
	case "earlier completion":
		at = start.Add(time.Millisecond)
	case "later completion":
		at = at.Add(time.Second)
	case "same instant different zone":
		at = at.In(time.FixedZone("test", 3600))
	}
	wantRows := int64(0)
	if mode == "first" {
		wantRows = 1
	}
	assertCompletionRowCount(t, f, in.AttemptID, proposed, at, wantRows)
	got, err := f.repo.Complete(t.Context(), in.AttemptID, proposed, at)
	if mode == "missing" {
		if !errors.Is(err, notifications.ErrAttemptNotFound) {
			t.Fatalf("missing completion=%v", err)
		}
		return
	}
	assertCompletionResult(t, f, mode, in.AttemptID, at, got, want, err)
	t.Logf("case=%s conditional_rows=%d", mode, wantRows)
}

func prepareCompletionContract(
	t *testing.T,
	f fixture,
	in notifications.SendInput,
	proposed notifications.Evidence,
	start, at time.Time,
	mode string,
) notifications.Attempt {
	t.Helper()
	if mode == "missing" {
		return notifications.Attempt{}
	}
	if _, _, err := f.repo.Admit(t.Context(), in, start, start.Add(notifications.SendTimeout)); err != nil {
		t.Fatal(err)
	}
	if mode == "first" {
		return notifications.Attempt{}
	}
	if mode == "after recovery" {
		if err := f.repo.Recover(t.Context(), at); err != nil {
			t.Fatal(err)
		}
		want, err := f.repo.GetAttempt(t.Context(), in.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		return want
	}
	want, err := f.repo.Complete(t.Context(), in.AttemptID, proposed, at)
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func assertCompletionResult(
	t *testing.T,
	f fixture,
	mode string,
	id notifications.AttemptID,
	at time.Time,
	got, want notifications.Attempt,
	err error,
) {
	t.Helper()
	conflict := mode != "first" && mode != "identical duplicate" && mode != "same instant different zone"
	if conflict {
		if !errors.Is(err, notifications.ErrCompletionConflict) || !reflect.DeepEqual(got, notifications.Attempt{}) {
			t.Fatalf("conflicting completion returned evidence: %+v %v", got, err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if mode == "first" {
		if got.Outcome != notifications.Accepted || got.Receipt != "first receipt" || got.CompletedAt == nil ||
			!got.CompletedAt.Equal(at) {
			t.Fatalf("first completion=%+v", got)
		}
		want = got
	}
	if !conflict && !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal evidence changed: got=%+v want=%+v", got, want)
	}
	stored, err := f.repo.GetAttempt(t.Context(), id)
	if err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("retained evidence changed: %+v %v", stored, err)
	}
}

func assertCompletionRowCount(
	t *testing.T,
	f fixture,
	id notifications.AttemptID,
	evidence notifications.Evidence,
	at time.Time,
	want int64,
) {
	t.Helper()
	// Observe the exact generated conditional statement in a rolled-back probe,
	// then exercise Complete separately against the same unchanged starting row.
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := dbsqlc.New(tx).CompleteAttempt(t.Context(), dbsqlc.CompleteAttemptParams{
		ID: string(id), Outcome: string(evidence.Outcome), ReasonCode: evidence.ReasonCode, Receipt: evidence.Receipt,
		CompletedAt: sql.NullString{String: at.UTC().Format("2006-01-02T15:04:05.000000000Z"), Valid: true},
	})
	if err != nil || rows != want {
		t.Fatalf("conditional rows=%d want=%d err=%v", rows, want, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
