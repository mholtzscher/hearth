//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

type fixture struct {
	path         string
	database     *sql.DB
	repo         *sqlite.Repository
	runtimes     *adapterssqlite.AdapterRepository
	registration notifications.Registration
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "core.db")
	database := dbtest.OpenMigrated(t, path)
	runtime := adapterssqlite.NewAdapterRepository(database)
	id := runtimeID(t)
	now := time.Now().UTC()
	if err := runtime.ClaimAdapterRuntime(
		context.Background(),
		adapters.ClaimRuntimeWrite{
			AdapterID:       "alerts",
			RuntimeID:       adapters.RuntimeID(id),
			SoftwareName:    "alerts",
			SoftwareVersion: "1",
			ClaimedAt:       now,
			LeaseExpiresAt:  now.Add(15 * time.Second),
		},
	); err != nil {
		t.Fatal(err)
	}
	return fixture{
		path:         path,
		database:     database,
		repo:         sqlite.NewRepository(database),
		runtimes:     runtime,
		registration: notifications.Registration{AdapterID: "alerts", RuntimeID: id, Key: "home", Name: "Home"},
	}
}
func runtimeID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return "run_" + id.String()
}
func input(t *testing.T, d notifications.DestinationID) notifications.SendInput {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return notifications.SendInput{
		AttemptID:     id,
		DestinationID: d,
		Message:       notifications.Message{Title: "Freezer", Text: "Too warm"},
	}
}
func register(t *testing.T, f fixture, r notifications.Registration) notifications.Destination {
	t.Helper()
	d, err := f.repo.Register(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A1: stable additive mappings survive runtime succession, while routing does not.
func TestRegistrationRuntimeFencingAndAdditiveIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	r := f.registration
	r.Name = " Renamed "
	again := register(t, f, r)
	if again.ID != d.ID || again.Name != "Renamed" || !again.Available {
		t.Fatalf("repeat: %+v", again)
	}
	r.Key = "other"
	other := register(t, f, r)
	if other.ID == d.ID {
		t.Fatal("distinct key reused identity")
	}
	if err := f.runtimes.ReleaseAdapterRuntime(
		ctx,
		adapters.ReleaseRuntimeWrite{
			AdapterID:  r.AdapterID,
			RuntimeID:  adapters.RuntimeID(r.RuntimeID),
			ReleasedAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Register(ctx, r); !errors.Is(err, notifications.ErrStaleRuntime) {
		t.Fatalf("stale registration: %v", err)
	}
	r.RuntimeID = runtimeID(t)
	now := time.Now().UTC()
	if err := f.runtimes.ClaimAdapterRuntime(
		ctx,
		adapters.ClaimRuntimeWrite{
			AdapterID:       r.AdapterID,
			RuntimeID:       adapters.RuntimeID(r.RuntimeID),
			SoftwareName:    "alerts",
			SoftwareVersion: "2",
			ClaimedAt:       now,
			LeaseExpiresAt:  now.Add(15 * time.Second),
		},
	); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.GetDestination(ctx, d.ID)
	if err != nil || before.Available {
		t.Fatalf("successor inherited old route: %+v %v", before, err)
	}
	r.Key = f.registration.Key
	successor := register(t, f, r)
	if successor.ID != d.ID || !successor.Available {
		t.Fatalf("successor: %+v", successor)
	}
	oldOther, err := f.repo.GetDestination(ctx, other.ID)
	if err != nil || oldOther.Available {
		t.Fatalf("omitted route: %+v %v", oldOther, err)
	}
	page, err := f.repo.ListDestinations(ctx, 1, "")
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page: %+v %v", page, err)
	}
	next, err := f.repo.ListDestinations(ctx, 1, page.NextCursor)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("next: %+v %v", next, err)
	}
}

// Snapshot, terminal CAS, crash recovery and retention are owned by real SQLite.
func TestAttemptSnapshotRecoveryAndRetention(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	service, serviceErr := notifications.NewService(
		ctx,
		f.repo,
		senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
			t.Error("retention dispatched a send")
			return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "transport_error"}
		}),
	)
	if serviceErr != nil {
		t.Fatal(serviceErr)
	}
	d := register(t, f, f.registration)
	start := time.Now().UTC().Add(-31 * 24 * time.Hour)
	in := input(t, d.ID)
	a, available, err := f.repo.Admit(ctx, in, start, start.Add(notifications.SendTimeout))
	if err != nil || !available || a.Outcome != "" {
		t.Fatalf("admit: %+v %v", a, err)
	}
	r := f.registration
	r.Name = "Changed"
	register(t, f, r)
	terminal, err := f.repo.Complete(
		ctx,
		a.ID,
		notifications.Evidence{Outcome: notifications.Accepted, Receipt: "receipt"},
		start.Add(time.Second),
	)
	if err != nil || terminal.DestinationName != "Home" {
		t.Fatalf("snapshot: %+v %v", terminal, err)
	}
	late, err := f.repo.Complete(
		ctx,
		a.ID,
		notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"},
		time.Now().UTC(),
	)
	if !errors.Is(err, notifications.ErrCompletionConflict) || late.ID != "" {
		t.Fatalf("late completion did not conflict: %+v %v", late, err)
	}
	retained, err := f.repo.GetAttempt(ctx, a.ID)
	if err != nil || retained.Outcome != notifications.Accepted || retained.Receipt != "receipt" {
		t.Fatalf("terminal changed: %+v %v", retained, err)
	}
	running := input(t, d.ID)
	if _, _, err = f.repo.Admit(ctx, running, start, start.Add(notifications.SendTimeout)); err != nil {
		t.Fatal(err)
	}
	if err = service.PruneHistory(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err = f.repo.GetAttempt(ctx, a.ID); !errors.Is(err, notifications.ErrAttemptNotFound) {
		t.Fatalf("pruned detail: %v", err)
	}
	if err = f.repo.Recover(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.repo.GetAttempt(ctx, running.AttemptID)
	if err != nil || recovered.Outcome != notifications.Unknown || recovered.ReasonCode != "core_restarted" {
		t.Fatalf("recover: %+v %v", recovered, err)
	}
	if err = service.PruneHistory(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("recovery completion retention: %v", err)
	}
	page, err := f.repo.ListAttempts(ctx, notifications.AttemptFilter{DestinationID: d.ID, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != running.AttemptID {
		t.Fatalf("history: %+v %v", page, err)
	}
}

func TestAdmissionRegistrationRaceKeepsCoherentSnapshot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			r := f.registration
			if i%2 == 0 {
				r.Name = "Updated"
			}
			if _, err := f.repo.Register(ctx, r); err != nil {
				t.Error(err)
			}
		})
		in := input(t, d.ID)
		wg.Go(func() {
			at := time.Now().UTC()
			a, available, err := f.repo.Admit(ctx, in, at, at.Add(notifications.SendTimeout))
			if err != nil {
				t.Error(err)
				return
			}
			if !available || a.Route.RuntimeID != f.registration.RuntimeID || a.Route.LocalKey != "home" ||
				(a.DestinationName != "Home" && a.DestinationName != "Updated") {
				t.Errorf("incoherent snapshot: %+v", a)
			}
		})
	}
	wg.Wait()
}

func TestRuntimeSuccessionRacingAdmissionCannotRetargetSnapshot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	successor := runtimeID(t)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := f.runtimes.ReleaseAdapterRuntime(
			ctx,
			adapters.ReleaseRuntimeWrite{
				AdapterID:  "alerts",
				RuntimeID:  adapters.RuntimeID(f.registration.RuntimeID),
				ReleasedAt: time.Now().UTC(),
			},
		); err != nil {
			t.Error(err)
			return
		}
		now := time.Now().UTC()
		if err := f.runtimes.ClaimAdapterRuntime(
			ctx,
			adapters.ClaimRuntimeWrite{
				AdapterID:       "alerts",
				RuntimeID:       adapters.RuntimeID(successor),
				SoftwareName:    "alerts",
				SoftwareVersion: "2",
				ClaimedAt:       now,
				LeaseExpiresAt:  now.Add(15 * time.Second),
			},
		); err != nil {
			t.Error(err)
			return
		}
		r := f.registration
		r.RuntimeID = successor
		r.Name = "Successor"
		if _, err := f.repo.Register(ctx, r); err != nil {
			t.Error(err)
		}
	})
	for range 30 {
		in := input(t, d.ID)
		wg.Go(func() {
			at := time.Now().UTC()
			a, _, err := f.repo.Admit(ctx, in, at, at.Add(notifications.SendTimeout))
			if err != nil {
				t.Error(err)
				return
			}
			wantName := map[string]string{successor: "Successor", f.registration.RuntimeID: "Home"}[a.Route.RuntimeID]
			if wantName == "" || a.DestinationName != wantName {
				t.Errorf("mixed previous snapshot: %+v", a)
			}
			saved, err := f.repo.GetAttempt(ctx, a.ID)
			if err != nil || saved.Route != a.Route || saved.DestinationName != a.DestinationName {
				t.Errorf("snapshot retargeted: %+v %+v %v", a, saved, err)
			}
		})
	}
	wg.Wait()
	d, err := f.repo.GetDestination(ctx, d.ID)
	if err != nil || !d.Available || d.Name != "Successor" {
		t.Fatalf("final route: %+v %v", d, err)
	}
}

func TestRepositoryRejectsInvalidIndependentInputsAndUnhealthyRuntime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	bad := f.registration
	bad.Name = " "
	if _, err := f.repo.Register(ctx, bad); !errors.Is(err, notifications.ErrInvalidInput) {
		t.Fatalf("invalid registration: %v", err)
	}
	in := input(t, d.ID)
	in.Message.Text = " "
	at := time.Now().UTC()
	if _, _, err := f.repo.Admit(
		ctx,
		in,
		at,
		at.Add(notifications.SendTimeout),
	); !errors.Is(
		err,
		notifications.ErrInvalidInput,
	) {
		t.Fatalf("invalid admission: %v", err)
	}
	if _, err := f.runtimes.RecordAdapterHeartbeat(
		ctx,
		adapters.HeartbeatWrite{
			AdapterID:        f.registration.AdapterID,
			RuntimeID:        adapters.RuntimeID(f.registration.RuntimeID),
			ReceivedAt:       at,
			SourceObservedAt: at,
			LeaseExpiresAt:   at.Add(15 * time.Second),
			ExternalStatus:   adapters.AdapterHealthUnhealthy,
			Reason:           &adapters.HealthReason{Code: "adapter.alerts.offline"},
		},
	); err != nil {
		t.Fatal(err)
	}
	in = input(t, d.ID)
	_, available, err := f.repo.Admit(ctx, in, at, at.Add(notifications.SendTimeout))
	if err != nil || available {
		t.Fatalf("unhealthy admission: %v %v", available, err)
	}
	if err = f.runtimes.ExpireAdapterLeases(
		ctx,
		adapters.ExpireLeasesWrite{ExpiresAt: at.Add(2 * time.Minute)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Register(ctx, f.registration); !errors.Is(err, notifications.ErrStaleRuntime) {
		t.Fatalf("expired registration: %v", err)
	}
}

//nolint:gocognit // One history fixture protects keyset scope and completion-based pruning.
func TestHistoryKeysetCursorIsBoundToFilterAndPrunesStrictlyBeforeCompletion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	r := f.registration
	r.Key = "other"
	other := register(t, f, r)
	cutoff := time.Now().UTC().Add(-notifications.Retention)
	start := cutoff.Add(-time.Minute)
	ids := make([]notifications.AttemptID, 0, 5)
	for _, destination := range []notifications.DestinationID{d.ID, other.ID, d.ID, other.ID, other.ID} {
		in := input(t, destination)
		if _, _, err := f.repo.Admit(ctx, in, start, start.Add(notifications.SendTimeout)); err != nil {
			t.Fatal(err)
		}
		completed := cutoff
		if destination == other.ID {
			completed = cutoff.Add(-time.Nanosecond)
		}
		if _, err := f.repo.Complete(
			ctx,
			in.AttemptID,
			notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"},
			completed,
		); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.AttemptID)
	}
	page, err := f.repo.ListAttempts(ctx, notifications.AttemptFilter{DestinationID: d.ID, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := f.repo.ListAttempts(
		ctx,
		notifications.AttemptFilter{DestinationID: d.ID, Limit: 1, Cursor: page.NextCursor},
	)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
		t.Fatalf("next page: %+v %v", next, err)
	}
	if page.Items[0].ID < next.Items[0].ID {
		t.Fatal("equal-time IDs not ordered descending")
	}
	if _, err = f.repo.ListAttempts(
		ctx,
		notifications.AttemptFilter{DestinationID: other.ID, Cursor: page.NextCursor},
	); !errors.Is(
		err,
		notifications.ErrInvalidInput,
	) {
		t.Fatalf("cursor filter changed: %v", err)
	}
	for _, badCursor := range []string{"not-base64%", strings.Repeat("a", 2049)} {
		if _, cursorErr := f.repo.ListAttempts(
			ctx,
			notifications.AttemptFilter{DestinationID: d.ID, Cursor: badCursor},
		); !errors.Is(
			cursorErr,
			notifications.ErrInvalidInput,
		) {
			t.Fatalf("invalid cursor accepted: %v", cursorErr)
		}
	}
	unknown, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	empty, err := f.repo.ListAttempts(ctx, notifications.AttemptFilter{DestinationID: unknown})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("unknown filtered ID: %+v %v", empty, err)
	}
	count, err := f.repo.DeleteHistoryBefore(ctx, cutoff, 2)
	if err != nil || count != 2 {
		t.Fatalf("bounded batch: %d %v", count, err)
	}
	count, err = f.repo.DeleteHistoryBefore(ctx, cutoff, 500)
	if err != nil || count != 1 {
		t.Fatalf("strict completion cutoff: %d %v", count, err)
	}
	if _, err = f.repo.GetAttempt(ctx, ids[0]); err != nil {
		t.Fatalf("cutoff-equal record removed: %v", err)
	}
}

func TestRepositoryMessageUnicodeBoundsBeforeRecording(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	d := register(t, f, f.registration)
	at := time.Now().UTC()
	cases := []struct {
		name, title, text string
		valid             bool
	}{
		{"unicode_limits", strings.Repeat("é", 256), strings.Repeat("暖", 2000), true},
		{"title_oversize", strings.Repeat("é", 257), "text", false},
		{"message_oversize", "", strings.Repeat("暖", 2001), false},
		{"blank", "", " \t\n", false},
		{"title_invalid_utf8", string([]byte{255}), "text", false},
		{"text_invalid_utf8", "", string([]byte{255}), false},
	}
	for _, tc := range cases {
		in := input(t, d.ID)
		in.Message = notifications.Message{Title: tc.title, Text: tc.text}
		_, _, err := f.repo.Admit(ctx, in, at, at.Add(notifications.SendTimeout))
		if tc.valid {
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		} else if !errors.Is(err, notifications.ErrInvalidInput) {
			t.Fatalf("%s accepted: %v", tc.name, err)
		}
	}
	page, err := f.repo.ListAttempts(ctx, notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("invalid messages persisted: %+v %v", page, err)
	}
}
