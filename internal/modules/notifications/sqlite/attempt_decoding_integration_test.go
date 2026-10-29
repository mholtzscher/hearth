//go:build integration

package sqlite_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestAttemptDecodingRejectsUninterpretableEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		assignment string
		value      string
	}{
		{"start representation", "started_at = ?", "private_stored_value"},
		{"deadline representation", "deadline = ?", "private_stored_value"},
		{"completion representation", "completed_at = ?", "private_stored_value"},
		{"reason vocabulary", "outcome = 'rejected', receipt = '', reason_code = ?", "private_stored_value"},
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

func TestAttemptListingPreservesRetainedIdentity(t *testing.T) {
	t.Parallel()
	f, attempt := retainedAttempt(t)
	if _, err := f.database.ExecContext(t.Context(),
		"UPDATE notification_attempts SET id = ? WHERE id = ?", "ntf_retained", attempt.ID,
	); err != nil {
		t.Fatal(err)
	}
	page, err := f.repo.ListAttempts(t.Context(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "ntf_retained" {
		t.Fatalf("listing reinterpreted retained identity: %+v, %v", page, err)
	}
}

func TestRetainedAttemptPolicyDoesNotBlockHistoryOrCompletion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	destination := register(t, f, f.registration)
	at := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	attempt, _, err := f.repo.Admit(t.Context(), input(t, destination.ID), at, at.Add(notifications.SendTimeout))
	if err != nil {
		t.Fatal(err)
	}
	// Retained snapshots need not satisfy today's registration, message-size,
	// or timeout policy. Reading them must not normalize or repair them.
	attempt.DestinationID = "ndst_retained"
	attempt.Route = notifications.RouteSnapshot{
		AdapterID: "retired adapter",
		LocalKey:  "retired key",
		RuntimeID: "run_retained",
	}
	attempt.Message = notifications.Message{Title: strings.Repeat("x", 257), Text: strings.Repeat("x", 2001)}
	attempt.Deadline = at.Add(time.Minute)
	if _, err = f.database.ExecContext(t.Context(), `UPDATE notification_attempts
		SET destination_id = ?, adapter_id = ?, local_key = ?, runtime_id = ?, title = ?, message = ?, deadline = ? WHERE id = ?`,
		attempt.DestinationID, attempt.Route.AdapterID, attempt.Route.LocalKey, attempt.Route.RuntimeID,
		attempt.Message.Title, attempt.Message.Text, attempt.Deadline.Format(time.RFC3339Nano), attempt.ID,
	); err != nil {
		t.Fatal(err)
	}
	assertRetainedAttempt(t, f, attempt)
	completed := at.Add(time.Second)
	if _, err = f.repo.Complete(t.Context(), attempt.ID,
		notifications.Evidence{Outcome: notifications.Accepted, Receipt: "receipt"}, completed); err != nil {
		t.Fatalf("historical policy rolled back completion: %v", err)
	}
	attempt.Outcome, attempt.Receipt, attempt.CompletedAt = notifications.Accepted, "receipt", &completed
	assertRetainedAttempt(t, f, attempt)
	// SQL already guarantees receipt/outcome column shape. Its historical size
	// is not part of interpreting provider acceptance.
	attempt.Receipt = strings.Repeat("r", 257)
	if _, err = f.database.ExecContext(t.Context(), "UPDATE notification_attempts SET receipt = ? WHERE id = ?",
		attempt.Receipt, attempt.ID); err != nil {
		t.Fatal(err)
	}
	assertRetainedAttempt(t, f, attempt)
}

func assertRetainedAttempt(t *testing.T, f fixture, want notifications.Attempt) {
	t.Helper()
	got, err := f.repo.GetAttempt(t.Context(), want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("retained attempt changed or failed to decode: %+v, %v", got, err)
	}
	page, err := f.repo.ListAttempts(t.Context(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0], want) {
		t.Fatalf("retained history changed or failed to decode: %+v, %v", page, err)
	}
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
