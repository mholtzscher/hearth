//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestDestinationValidationSeparatesDisplayFromAdmission(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, column, value string
		readable            bool
	}{
		{"blank name", "name", " ", false},
		{"name bound", "name", strings.Repeat("x", 201), false},
		{"route key", "local_key", "private stored value", true},
		{"adapter identity", "adapter_id", "private stored value", true},
		{"runtime identity", "runtime_id", "run_private_stored_value", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			d := register(t, f, f.registration)
			service, err := notifications.NewService(t.Context(), f.repo,
				senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
					t.Error("corrupt destination reached dispatch")
					return notifications.Evidence{Outcome: notifications.Accepted}
				}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.database.ExecContext(t.Context(),
				"UPDATE notification_destinations SET "+test.column+" = ? WHERE id = ?", test.value, d.ID,
			); err != nil {
				t.Fatal(err)
			}
			assertDestinationReadability(t, f, d, test.readable)
			at := time.Now().UTC()
			_, _, err = f.repo.Admit(t.Context(), input(t, d.ID), at, at.Add(notifications.SendTimeout))
			assertDestinationPersistenceFailure(t, err)
			_, err = service.SendPublic(t.Context(), input(t, d.ID))
			assertDestinationPersistenceFailure(t, err)
			var count int
			if err = f.database.QueryRowContext(t.Context(), "SELECT count(*) FROM notification_attempts").
				Scan(&count); err != nil ||
				count != 0 {
				t.Fatalf("corrupt destination admitted attempts: count=%d, %v", count, err)
			}
		})
	}
}

func assertDestinationReadability(t *testing.T, f fixture, destination notifications.Destination, readable bool) {
	t.Helper()
	got, getErr := f.repo.GetDestination(t.Context(), destination.ID)
	page, listErr := f.repo.ListDestinations(t.Context(), 10, "")
	if !readable {
		assertDestinationPersistenceFailure(t, getErr)
		assertDestinationPersistenceFailure(t, listErr)
		return
	}
	if getErr != nil || got.ID != destination.ID || got.Name != destination.Name {
		t.Fatalf("route corruption blocked or changed public metadata: %+v, %v", got, getErr)
	}
	if listErr != nil || len(page.Items) != 1 || page.Items[0] != got {
		t.Fatalf("route corruption blocked or changed listing: %+v, %v", page, listErr)
	}
}

func TestDestinationListRejectsMalformedStoredIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	d := register(t, f, f.registration)
	if _, err := f.database.ExecContext(t.Context(),
		"UPDATE notification_destinations SET id = ? WHERE id = ?", "ndst_private_stored_value", d.ID,
	); err != nil {
		t.Fatal(err)
	}
	_, err := f.repo.ListDestinations(t.Context(), 10, "")
	assertDestinationPersistenceFailure(t, err)
	// Upsert must also roll back if the retained stable identity is corrupt.
	_, err = f.repo.Register(t.Context(), f.registration)
	assertDestinationPersistenceFailure(t, err)
}

func assertDestinationPersistenceFailure(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, notifications.ErrPersistence) || errors.Is(err, notifications.ErrInvalidInput) {
		t.Fatalf("destination error = %v, want internal persistence failure", err)
	}
	var diagnostic interface{ Cause() error }
	if !errors.As(err, &diagnostic) || diagnostic.Cause() == nil ||
		strings.Contains(err.Error(), "private_stored_value") {
		t.Fatalf("destination error lost its private cause or exposed retained data: %v", err)
	}
}
