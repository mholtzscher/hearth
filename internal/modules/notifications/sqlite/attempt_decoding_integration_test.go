//go:build integration

package sqlite_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestAttemptDecodingRejectsSQLValidCorruption(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		assignment string
		value      string
	}{
		{"destination identity", "destination_id = ?", "ndst_private_stored_value"},
		{"runtime identity", "runtime_id = ?", "run_private_stored_value"},
		{"adapter identity", "adapter_id = ?", "private stored value"},
		{"route key", "local_key = ?", "private stored value"},
		{"destination name", "destination_name = ?", " "},
		{"message", "message = ?", " "},
		{"title bound", "title = ?", strings.Repeat("x", 257)},
		{"start representation", "started_at = ?", "private_stored_value"},
		{"zero start", "started_at = ?", "0001-01-01T00:00:00.000000000Z"},
		{"deadline representation", "deadline = ?", "private_stored_value"},
		{"deadline contract", "deadline = ?", "2026-10-29T12:00:11.120000000Z"},
		{"completion representation", "completed_at = ?", "private_stored_value"},
		{"zero completion", "completed_at = ?", "0001-01-01T00:00:00.000000000Z"},
		{"reason vocabulary", "outcome = 'rejected', receipt = '', reason_code = ?", "private_stored_value"},
		{"receipt bound", "receipt = ?", strings.Repeat("x", 257)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f, attempt := retainedAttempt(t)
			// These values satisfy SQL constraints. This tests the decoder's
			// remaining domain responsibility without disabling database checks.
			if _, err := f.database.ExecContext(t.Context(),
				"UPDATE notification_attempts SET "+test.assignment+" WHERE id = ?", test.value, attempt.ID,
			); err != nil {
				t.Fatal(err)
			}
			_, getErr := f.repo.GetAttempt(t.Context(), attempt.ID)
			assertStoredAttemptFailure(t, getErr)
			_, listErr := f.repo.ListAttempts(t.Context(), notifications.AttemptFilter{})
			assertStoredAttemptFailure(t, listErr)
			_, completeErr := f.repo.Complete(t.Context(), attempt.ID,
				notifications.Evidence{Outcome: notifications.Accepted}, attempt.StartedAt.Add(time.Second))
			assertStoredAttemptFailure(t, completeErr)
		})
	}
}

func TestAttemptListingRejectsMalformedStoredAttemptIdentity(t *testing.T) {
	t.Parallel()
	f, attempt := retainedAttempt(t)
	if _, err := f.database.ExecContext(t.Context(),
		"UPDATE notification_attempts SET id = ? WHERE id = ?", "ntf_private_stored_value", attempt.ID,
	); err != nil {
		t.Fatal(err)
	}
	_, err := f.repo.ListAttempts(t.Context(), notifications.AttemptFilter{})
	assertStoredAttemptFailure(t, err)
}

func TestAttemptDecodingPreservesStoredTimestampRepresentations(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"2006-01-02T15:04:05.000000000Z07:00", time.RFC3339Nano} {
		t.Run(layout, func(t *testing.T) {
			t.Parallel()
			assertStoredAttemptTimeFormat(t, layout)
		})
	}
}

func assertStoredAttemptTimeFormat(t *testing.T, layout string) {
	t.Helper()
	f := newFixture(t)
	d := register(t, f, f.registration)
	in := input(t, d.ID)
	at := time.Date(2026, time.October, 29, 12, 0, 0, 120000000, time.UTC)
	attempt, _, err := f.repo.Admit(t.Context(), in, at, at.Add(notifications.SendTimeout))
	if err != nil {
		t.Fatal(err)
	}
	running, err := f.repo.GetAttempt(t.Context(), attempt.ID)
	if err != nil || running.CompletedAt != nil || running.Outcome != "" || running.Route != attempt.Route ||
		running.Message != in.Message {
		t.Fatalf("running snapshot = %+v, %v", running, err)
	}
	// A backward wall-clock correction is diagnostic evidence, not
	// proof that a legal completion happened before its admission.
	completed := at.Add(-time.Second)
	if _, err = f.repo.Complete(t.Context(), attempt.ID,
		notifications.Evidence{Outcome: notifications.Accepted, Receipt: "receipt"}, completed,
	); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("numeric-offset", -4*60*60)
	startText := at.In(zone).Format(layout)
	deadlineText := at.Add(notifications.SendTimeout).In(zone).Format(layout)
	completionText := completed.In(zone).Format(layout)
	if _, err = f.database.ExecContext(t.Context(),
		"UPDATE notification_attempts SET started_at = ?, deadline = ?, completed_at = ? WHERE id = ?",
		startText, deadlineText, completionText, attempt.ID,
	); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetAttempt(t.Context(), attempt.ID)
	if err != nil || !got.StartedAt.Equal(at) || !got.Deadline.Equal(at.Add(notifications.SendTimeout)) ||
		got.CompletedAt == nil || !got.CompletedAt.Equal(completed) || got.Outcome != notifications.Accepted ||
		got.Receipt != "receipt" || got.Route != attempt.Route || got.Message != in.Message ||
		got.DestinationName != "Home" {
		t.Fatalf("retained snapshot = %+v, %v", got, err)
	}
	page, err := f.repo.ListAttempts(t.Context(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != attempt.ID ||
		!page.Items[0].StartedAt.Equal(at) {
		t.Fatalf("retained listing = %+v, %v", page, err)
	}
	var stored string
	if err = f.database.QueryRowContext(t.Context(),
		"SELECT started_at FROM notification_attempts WHERE id = ?", attempt.ID,
	).Scan(&stored); err != nil || stored != startText {
		t.Fatalf("read changed retained timestamp: %q, %v", stored, err)
	}
}

func TestAttemptTimestampWritesStayCanonicalUTC(t *testing.T) {
	t.Parallel()
	f, attempt := retainedAttempt(t)
	var start, deadline, completed string
	if err := f.database.QueryRowContext(t.Context(),
		"SELECT started_at, deadline, completed_at FROM notification_attempts WHERE id = ?", attempt.ID,
	).Scan(&start, &deadline, &completed); err != nil {
		t.Fatal(err)
	}
	if start != "2026-10-29T12:00:00.120000000Z" || deadline != "2026-10-29T12:00:10.120000000Z" ||
		completed != "2026-10-29T12:00:01.120000000Z" {
		t.Fatalf("noncanonical timestamp writes: start=%q deadline=%q completed=%q", start, deadline, completed)
	}
}

func retainedAttempt(t *testing.T) (fixture, notifications.Attempt) {
	t.Helper()
	f := newFixture(t)
	d := register(t, f, f.registration)
	at := time.Date(2026, time.October, 29, 12, 0, 0, 120000000, time.UTC)
	in := input(t, d.ID)
	a, _, err := f.repo.Admit(t.Context(), in, at, at.Add(notifications.SendTimeout))
	if err != nil {
		t.Fatal(err)
	}
	a, err = f.repo.Complete(t.Context(), a.ID,
		notifications.Evidence{Outcome: notifications.Accepted, Receipt: "receipt"}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return f, a
}

func assertStoredAttemptFailure(t *testing.T, err error) {
	t.Helper()
	wrapped := fmt.Errorf("read retained attempt: %w", err)
	if !errors.Is(wrapped, notifications.ErrPersistence) || errors.Is(wrapped, notifications.ErrInvalidInput) {
		t.Fatalf("stored-row error = %v, want persistence failure without invalid-input identity", err)
	}
	var diagnostic interface{ Cause() error }
	if !errors.As(wrapped, &diagnostic) || diagnostic.Cause() == nil {
		t.Fatalf("stored-row error lost its private decoding cause: %v", err)
	}
	if strings.Contains(err.Error(), "private_stored_value") {
		t.Fatalf("stored-row error exposes retained values: %v", err)
	}
}
