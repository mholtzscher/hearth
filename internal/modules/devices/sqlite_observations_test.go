package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestObservationProjectionAdvancesStateByReceiveOrderAndDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openMigratedDatabase(t, path)
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := NewService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	first := newObservation(t, entityID, `true`, observedAt.Add(-time.Minute))
	result, err := service.ProjectObservation(ctx, "simulator", first, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied || result.State == nil || string(result.State.Value) != "true" {
		t.Fatalf("first projection = %#v", result)
	}

	olderSource := observedAt.Add(-24 * time.Hour)
	second := newObservation(t, entityID, `true`, observedAt.Add(-2*time.Hour))
	second.SourceUpdatedAt = &olderSource
	result, err = service.ProjectObservation(ctx, "simulator", second, observedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionUnchanged || result.State == nil ||
		result.State.ReceiveOrder <= 1 || result.State.ObservationID != second.ID ||
		!result.State.AdapterReceivedAt.Equal(second.AdapterReceivedAt) {
		t.Fatalf("same-value projection = %#v", result)
	}

	redelivery := second
	redelivery.Value = Value(`false`)
	result, err = service.ProjectObservation(ctx, "simulator", redelivery, observedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionDuplicate || result.State != nil {
		t.Fatalf("duplicate projection = %#v", result)
	}

	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != second.ID || string(view.State.Value) != "true" ||
		view.Entity.Name != "Power" || view.Entity.AdapterID != "simulator" {
		t.Fatalf("entity view = %#v", view)
	}
	assertReceiptCount(t, database, 2)

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openMigratedDatabase(t, path)
	restarted := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	view, err = restarted.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != second.ID ||
		view.State.ReceiveOrder != resultReceiveOrder(t, database, second.ID) {
		t.Fatalf("restarted entity view = %#v", view)
	}
}

//nolint:paralleltest // Subtests share one repository and are asserted as a batch.
func TestObservationProjectionDurablyRejectsIdentityAndValueFailures(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	unknownEntityID, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		adapterID   string
		observation Observation
		want        ObservationRejection
	}{
		{"unknown entity", "simulator", newObservation(t, unknownEntityID, `true`, observedAt), RejectionUnknownEntity},
		{"wrong adapter", "other-adapter", newObservation(t, entityID, `true`, observedAt), RejectionWrongAdapter},
		{"invalid value", "simulator", newObservation(t, entityID, `1`, observedAt), RejectionInvalidValue},
	}
	//nolint:paralleltest // Cases share one repository and are asserted as a batch.
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, projectionErr := service.ProjectObservation(
				ctx,
				test.adapterID,
				test.observation,
				observedAt.Add(time.Duration(index)*time.Second),
			)
			if projectionErr != nil {
				t.Fatal(projectionErr)
			}
			if result.Disposition != DispositionRejected || result.Rejection == nil || *result.Rejection != test.want ||
				result.State != nil {
				t.Fatalf("projection = %#v", result)
			}
		})
	}
	assertReceiptCount(t, database, len(tests))
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != nil {
		t.Fatalf("rejected observation created state: %#v", view.State)
	}
}

func TestDisabledEntityRejectsUnlinkedObservationAndAllowsActiveCommandRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := NewService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	baseline := newObservation(t, entityID, `true`, now)
	if _, err := service.ProjectObservation(ctx, "simulator", baseline, now); err != nil {
		t.Fatal(err)
	}

	active := newCommandRecord(t, entityID, now.Add(time.Second))
	active.Parameters = CommandParameters(`{"value":false}`)
	if _, err := repository.CreateCommand(ctx, active); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := service.SetEntityEnabled(ctx, entityID, false); err != nil {
		t.Fatal(err)
	}

	rejected := newObservation(t, entityID, `1`, now)
	result, err := service.ProjectObservation(ctx, "simulator", rejected, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionEntityDisabled || result.State != nil {
		t.Fatalf("disabled projection = %#v", result)
	}
	duplicate, err := service.ProjectObservation(ctx, "simulator", rejected, now.Add(time.Second))
	if err != nil || duplicate.Disposition != DispositionDuplicate {
		t.Fatalf("disabled duplicate = %#v, %v", duplicate, err)
	}
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != baseline.ID || string(view.State.Value) != "true" {
		t.Fatalf("State after disabled rejection = %#v", view.State)
	}

	refresh := newObservation(t, entityID, `false`, now)
	refresh.RefreshForCommand = &active.ID
	result, err = service.ProjectObservation(ctx, "simulator", refresh, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied || result.State == nil || result.SatisfiedCommand == nil ||
		result.SatisfiedCommand.CommandID != active.ID {
		t.Fatalf("active refresh projection = %#v", result)
	}
	stored, err := repository.GetCommand(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusSatisfied || stored.OutcomeObservationID == nil ||
		*stored.OutcomeObservationID != refresh.ID {
		t.Fatalf("active Command = %#v", stored)
	}
}

func TestDisabledEntityDoesNotExemptUnknownExpiredOrTerminalCommandLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 20, 0, time.UTC)
	service := NewService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	expired := newCommandRecord(t, entityID, now.Add(-20*time.Second))
	if _, err := repository.CreateCommand(ctx, expired); err != nil {
		t.Fatal(err)
	}
	terminal := newCommandRecord(t, entityID, now.Add(-time.Second))
	if _, err := repository.CreateCommand(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCommand(ctx, CommandCompletion{
		ID: terminal.ID, Status: CommandStatusOutcomeTimeout, CompletedAt: now,
		FailureCode: CommandFailureOutcomeTimeout,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEntityEnabled(ctx, entityID, false); err != nil {
		t.Fatal(err)
	}
	unknown, err := NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []CommandID{unknown, expired.ID, terminal.ID} {
		observation := newObservation(t, entityID, `true`, now)
		observation.RefreshForCommand = &id
		result, projectionErr := service.ProjectObservation(
			ctx,
			"simulator",
			observation,
			now.Add(time.Duration(index)*time.Second),
		)
		if projectionErr != nil {
			t.Fatal(projectionErr)
		}
		if result.Disposition != DispositionRejected || result.Rejection == nil ||
			*result.Rejection != RejectionEntityDisabled || result.State != nil || result.SatisfiedCommand != nil {
			t.Fatalf("linked disabled projection %d = %#v", index, result)
		}
	}
}

func TestObservationProjectionSatisfiesOnlyMatchingActiveLinkedCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	completedAt := time.Date(2026, 8, 22, 12, 0, 2, 0, time.UTC)
	now := completedAt
	service := NewService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	requestedAt := completedAt.Add(-time.Second)
	command := newCommandRecord(t, entityID, requestedAt)
	if _, err := repository.CreateCommand(ctx, command); err != nil {
		t.Fatal(err)
	}

	mismatch := newObservation(t, entityID, `false`, requestedAt)
	mismatch.RefreshForCommand = &command.ID
	result, err := service.ProjectObservation(ctx, "simulator", mismatch, requestedAt.Add(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if result.SatisfiedCommand != nil {
		t.Fatalf("mismatched observation satisfied command: %#v", result.SatisfiedCommand)
	}
	stored, err := repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusRequested {
		t.Fatalf("mismatched command status = %q", stored.Status)
	}

	matching := newObservation(t, entityID, `true`, requestedAt.Add(time.Second))
	matching.RefreshForCommand = &command.ID
	result, err = service.ProjectObservation(ctx, "simulator", matching, requestedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.SatisfiedCommand == nil || result.SatisfiedCommand.CommandID != command.ID ||
		result.SatisfiedCommand.ObservationID != matching.ID || string(result.SatisfiedCommand.Value) != "true" {
		t.Fatalf("matching projection = %#v", result)
	}
	stored, err = repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusSatisfied || stored.CompletedAt == nil || !stored.CompletedAt.Equal(completedAt) ||
		stored.OutcomeObservationID == nil || *stored.OutcomeObservationID != matching.ID {
		t.Fatalf("satisfied command = %#v", stored)
	}

	late := newCommandRecord(t, entityID, requestedAt.Add(2*time.Minute))
	if _, err := repository.CreateCommand(ctx, late); err != nil {
		t.Fatal(err)
	}
	now = late.DeadlineAt.Add(time.Nanosecond)
	lateObservation := newObservation(t, entityID, `true`, now)
	lateObservation.RefreshForCommand = &late.ID
	result, err = service.ProjectObservation(ctx, "simulator", lateObservation, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.SatisfiedCommand != nil || result.State == nil || result.State.ObservationID != lateObservation.ID {
		t.Fatalf("post-deadline linked projection = %#v", result)
	}
	stored, err = repository.GetCommand(ctx, late.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusRequested || stored.CompletedAt != nil || stored.OutcomeObservationID != nil {
		t.Fatalf("post-deadline command = %#v", stored)
	}

	terminal := newCommandRecord(t, entityID, requestedAt.Add(time.Minute))
	if _, err := repository.CreateCommand(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if err := repository.InterruptActiveCommands(ctx, completedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	linked := newObservation(t, entityID, `true`, completedAt.Add(time.Minute))
	linked.RefreshForCommand = &terminal.ID
	result, err = service.ProjectObservation(ctx, "simulator", linked, completedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.SatisfiedCommand != nil || result.State == nil || result.State.ObservationID != linked.ID {
		t.Fatalf("terminal linked projection = %#v", result)
	}
	stored, err = repository.GetCommand(ctx, terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusInterrupted {
		t.Fatalf("terminal command status = %q", stored.Status)
	}
}

func TestReceiptPruningPinsCurrentStateUntilItAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := NewService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first := newObservation(t, entityID, `false`, start)
	second := newObservation(t, entityID, `true`, start.Add(time.Hour))
	if _, err := service.ProjectObservation(ctx, "simulator", first, start); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProjectObservation(ctx, "simulator", second, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	cutoff := start.Add(ObservationReceiptRetention + 2*time.Hour)
	if err := service.DeleteExpiredObservationReceipts(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	assertReceiptIDs(t, database, []ObservationID{second.ID})

	third := newObservation(t, entityID, `false`, cutoff)
	if _, err := service.ProjectObservation(ctx, "simulator", third, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteExpiredObservationReceipts(ctx, cutoff.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertReceiptIDs(t, database, []ObservationID{third.ID})
}

func newObservation(t *testing.T, entityID EntityID, value string, adapterReceivedAt time.Time) Observation {
	t.Helper()
	id, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return Observation{ID: id, EntityID: entityID, Value: Value(value), AdapterReceivedAt: adapterReceivedAt}
}

func assertReceiptCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("receipt count = %d, want %d", got, want)
	}
}

func assertReceiptIDs(t *testing.T, database *sql.DB, want []ObservationID) {
	t.Helper()
	rows, err := database.Query("SELECT observation_id FROM observation_receipts ORDER BY receive_order")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []ObservationID
	for rows.Next() {
		var id ObservationID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("receipt IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("receipt IDs = %v, want %v", got, want)
		}
	}
}

func resultReceiveOrder(t *testing.T, database *sql.DB, id ObservationID) int64 {
	t.Helper()
	var order int64
	if err := database.QueryRow("SELECT receive_order FROM observation_receipts WHERE observation_id = ?", id).
		Scan(&order); err != nil {
		t.Fatal(err)
	}
	return order
}
