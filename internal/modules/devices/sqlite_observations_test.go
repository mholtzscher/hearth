package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObservationProjectionAdvancesStateByReceiveOrderAndDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	first := newObservation(t, entityID, `true`, observedAt.Add(-time.Minute))
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, first, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied || result.State == nil || string(result.State.Value) != "true" {
		t.Fatalf("first projection = %#v", result)
	}

	olderSource := observedAt.Add(-24 * time.Hour)
	second := newObservation(t, entityID, `true`, observedAt.Add(-2*time.Hour))
	second.SourceUpdatedAt = &olderSource
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, second, observedAt.Add(time.Second),
	)
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
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, redelivery, observedAt.Add(2*time.Second),
	)
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

	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	database = openRegistrationDatabase(t, path)
	restarted := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	view, err = restarted.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != second.ID ||
		view.State.ReceiveOrder != resultReceiveOrder(t, database, second.ID) {
		t.Fatalf("restarted entity view = %#v", view)
	}
}

func TestObservationProjectionDurablyRejectsIdentityAndValueFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
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
		{
			"wrong adapter", "homeassistant", newObservation(t, entityID, `true`, observedAt), RejectionWrongAdapter,
		},
		{"invalid value", "simulator", newObservation(t, entityID, `1`, observedAt), RejectionInvalidValue},
	}
	for index, test := range tests {
		result, projectionErr := service.ProjectObservation(
			ctx,
			test.adapterID,
			testAdapterRuntime(test.adapterID),
			test.observation,
			observedAt.Add(time.Duration(index)*time.Second),
		)
		if projectionErr != nil {
			t.Fatalf("%s: %v", test.name, projectionErr)
		}
		if result.Disposition != DispositionRejected || result.Rejection == nil || *result.Rejection != test.want ||
			result.State != nil {
			t.Fatalf("%s: projection = %#v", test.name, result)
		}
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
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	claimTestAdapterRuntime(t, repository, now)
	entityID := binding.Entities[0].EntityID
	baseline := newObservation(t, entityID, `true`, now)
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, baseline, now,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}

	active := newCommandRecord(t, entityID, now.Add(time.Second))
	active.Parameters = CommandParameters(`{"value":false}`)
	if _, createErr := repository.CreateCommand(ctx, active); createErr != nil {
		t.Fatal(createErr)
	}
	now = now.Add(2 * time.Second)
	if _, enablementErr := service.SetEntityEnabled(ctx, entityID, false); enablementErr != nil {
		t.Fatal(enablementErr)
	}

	rejected := newObservation(t, entityID, `1`, now)
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, rejected, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionEntityDisabled || result.State != nil {
		t.Fatalf("disabled projection = %#v", result)
	}
	duplicate, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, now.Add(time.Second),
	)
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
	result, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, refresh, now)
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
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 20, 0, time.UTC)
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	claimTestAdapterRuntime(t, repository, now)
	entityID := binding.Entities[0].EntityID
	expired := newCommandRecord(t, entityID, now.Add(-20*time.Second))
	if _, createErr := repository.CreateCommand(ctx, expired); createErr != nil {
		t.Fatal(createErr)
	}
	terminal := newCommandRecord(t, entityID, now.Add(-time.Second))
	if _, createErr := repository.CreateCommand(ctx, terminal); createErr != nil {
		t.Fatal(createErr)
	}
	if completionErr := repository.CompleteCommand(ctx, CommandCompletion{
		ID: terminal.ID, Status: CommandStatusOutcomeTimeout, CompletedAt: now,
		FailureCode: CommandFailureOutcomeTimeout,
	}); completionErr != nil {
		t.Fatal(completionErr)
	}
	if _, enablementErr := service.SetEntityEnabled(ctx, entityID, false); enablementErr != nil {
		t.Fatal(enablementErr)
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
			testRuntimeID,
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

//nolint:gocognit,gocyclo,cyclop // The linked-command transition matrix is clearer as one persistence test.
func TestObservationProjectionSatisfiesOnlyMatchingActiveLinkedCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	completedAt := time.Date(2026, 8, 22, 12, 0, 2, 0, time.UTC)
	now := completedAt
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	claimTestAdapterRuntime(t, repository, completedAt)
	entityID := binding.Entities[0].EntityID
	requestedAt := completedAt.Add(-time.Second)
	command := newCommandRecord(t, entityID, requestedAt)
	if _, createErr := repository.CreateCommand(ctx, command); createErr != nil {
		t.Fatal(createErr)
	}

	mismatch := newObservation(t, entityID, `false`, requestedAt)
	mismatch.RefreshForCommand = &command.ID
	result, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, mismatch, requestedAt.Add(500*time.Millisecond),
	)
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
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, matching, requestedAt.Add(time.Second),
	)
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
	if _, createErr := repository.CreateCommand(ctx, late); createErr != nil {
		t.Fatal(createErr)
	}
	now = late.DeadlineAt.Add(time.Nanosecond)
	lateObservation := newObservation(t, entityID, `true`, now)
	lateObservation.RefreshForCommand = &late.ID
	result, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, lateObservation, now)
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
	if _, createErr := repository.CreateCommand(ctx, terminal); createErr != nil {
		t.Fatal(createErr)
	}
	if interruptErr := repository.InterruptActiveCommands(ctx, completedAt.Add(time.Minute)); interruptErr != nil {
		t.Fatal(interruptErr)
	}
	linked := newObservation(t, entityID, `true`, completedAt.Add(time.Minute))
	linked.RefreshForCommand = &terminal.ID
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, linked, completedAt.Add(time.Minute),
	)
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

//nolint:gocognit,gocyclo,cyclop // One timeline verifies receipt, deduplication, State, and Command effects.
func TestObservationRuntimeFencingRecordsStaleReceiptAndIsolatesCommands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	service := newTestService(repository, nil, catalog, Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	command := newCommandRecord(t, entityID, now.Add(time.Second))
	command, err = repository.CreateCommand(ctx, command)
	if err != nil || command.RuntimeID == nil || *command.RuntimeID != testRuntimeID {
		t.Fatalf("old-runtime Command = %#v, %v", command, err)
	}
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, ReleaseRuntimeWrite{
		AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: now.Add(2 * time.Second),
	}); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if claimErr := repository.ClaimAdapterRuntime(
		ctx,
		testClaimWrite(testSecondRuntime, now.Add(3*time.Second)),
	); claimErr != nil {
		t.Fatal(claimErr)
	}
	now = now.Add(4 * time.Second)

	stale := newObservation(t, entityID, `true`, now)
	stale.RefreshForCommand = &command.ID
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, stale, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionStaleRuntime || result.State != nil || result.SatisfiedCommand != nil {
		t.Fatalf("stale projection = %#v", result)
	}
	var receiptRuntime, rejectionCode string
	if scanErr := database.QueryRowContext(ctx, `
		SELECT runtime_id, rejection_code
		FROM observation_receipts
		WHERE observation_id = ?`, stale.ID,
	).Scan(&receiptRuntime, &rejectionCode); scanErr != nil {
		t.Fatal(scanErr)
	}
	if receiptRuntime != string(testRuntimeID) || rejectionCode != string(RejectionStaleRuntime) {
		t.Fatalf("stale receipt = %q/%q", receiptRuntime, rejectionCode)
	}

	unknownRuntime := RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ae")
	unknownRuntimeObservation := newObservation(t, entityID, `false`, now)
	unknownResult, err := service.ProjectObservation(
		ctx, "simulator", unknownRuntime, unknownRuntimeObservation, now,
	)
	if err != nil || unknownResult.Disposition != DispositionRejected || unknownResult.Rejection == nil ||
		*unknownResult.Rejection != RejectionStaleRuntime {
		t.Fatalf("unknown-runtime projection = %#v, %v", unknownResult, err)
	}
	var unknownReceiptRuntime sql.NullString
	if scanErr := database.QueryRowContext(ctx, `
		SELECT runtime_id FROM observation_receipts WHERE observation_id = ?`, unknownRuntimeObservation.ID,
	).Scan(&unknownReceiptRuntime); scanErr != nil {
		t.Fatal(scanErr)
	}
	if unknownReceiptRuntime.Valid {
		t.Fatalf("unknown-runtime receipt retained invalid foreign key %q", unknownReceiptRuntime.String)
	}

	duplicate, err := service.ProjectObservation(ctx, "simulator", testSecondRuntime, stale, now.Add(time.Second))
	if err != nil || duplicate.Disposition != DispositionDuplicate || duplicate.State != nil {
		t.Fatalf("cross-runtime redelivery = %#v, %v", duplicate, err)
	}
	fresh := newObservation(t, entityID, `true`, now.Add(time.Second))
	fresh.RefreshForCommand = &command.ID
	result, err = service.ProjectObservation(
		ctx, "simulator", testSecondRuntime, fresh, now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied || result.State == nil ||
		result.State.ObservationID != fresh.ID || result.SatisfiedCommand != nil {
		t.Fatalf("replacement projection = %#v", result)
	}
	stored, err := repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusRequested || stored.OutcomeObservationID != nil {
		t.Fatalf("old-runtime Command changed = %#v", stored)
	}
}

func TestReceiptPruningPinsCurrentStateUntilItAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first := newObservation(t, entityID, `false`, start)
	second := newObservation(t, entityID, `true`, start.Add(time.Hour))
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, first, start,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, second, start.Add(time.Hour),
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}

	cutoff := start.Add(ObservationReceiptRetention + 2*time.Hour)
	if pruneErr := service.DeleteExpiredObservationReceipts(ctx, cutoff); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertReceiptIDs(t, database, []ObservationID{second.ID})

	third := newObservation(t, entityID, `false`, cutoff)
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, third, cutoff,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if pruneErr := service.DeleteExpiredObservationReceipts(ctx, cutoff.Add(time.Second)); pruneErr != nil {
		t.Fatal(pruneErr)
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
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		got = append(got, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
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

func TestObservationProjectionEnrichesReceiptsWithNormalizedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	sourceUpdatedAt := observedAt.Add(-time.Hour)

	applied := newObservation(t, entityID, `true`, observedAt)
	applied.SourceUpdatedAt = &sourceUpdatedAt
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, applied, observedAt,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	assertReceiptRow(t, database, applied.ID, "applied", `true`, "", sourceUpdatedAt)

	unchanged := newObservation(t, entityID, `true`, observedAt.Add(time.Second))
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, unchanged, observedAt.Add(time.Second),
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	assertReceiptRow(t, database, unchanged.ID, "unchanged", `true`, "", time.Time{})

	rejected := newObservation(t, entityID, `1`, observedAt.Add(2*time.Second))
	rejected.SourceUpdatedAt = &sourceUpdatedAt
	result, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, observedAt.Add(2*time.Second),
	)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionInvalidValue {
		t.Fatalf("invalid-value projection = %#v", result)
	}
	assertReceiptRow(t, database, rejected.ID, "rejected", "", string(RejectionInvalidValue), sourceUpdatedAt)

	unknownEntityID, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	unknown := newObservation(t, unknownEntityID, `true`, observedAt.Add(3*time.Second))
	unknown.SourceUpdatedAt = &sourceUpdatedAt
	result, projectionErr = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, unknown, observedAt.Add(3*time.Second),
	)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if result.Disposition != DispositionRejected || result.Rejection == nil ||
		*result.Rejection != RejectionUnknownEntity {
		t.Fatalf("unknown-entity projection = %#v", result)
	}
	assertReceiptRow(t, database, unknown.ID, "rejected", "", string(RejectionUnknownEntity), sourceUpdatedAt)

	redelivery := unchanged
	redelivery.Value = Value(`false`)
	redelivery.SourceUpdatedAt = &sourceUpdatedAt
	result, projectionErr = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, redelivery, observedAt.Add(4*time.Second),
	)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if result.Disposition != DispositionDuplicate {
		t.Fatalf("duplicate projection = %#v", result)
	}
	assertReceiptCount(t, database, 4)
}

func assertReceiptRow(
	t *testing.T,
	database *sql.DB,
	id ObservationID,
	disposition, value, rejection string,
	sourceUpdatedAt time.Time,
) {
	t.Helper()
	var storedDisposition string
	var storedValue, storedRejection, storedSource sql.NullString
	if err := database.QueryRow(`
		SELECT disposition, state_value_json, rejection_code, source_updated_at
		FROM observation_receipts
		WHERE observation_id = ?`, id,
	).Scan(&storedDisposition, &storedValue, &storedRejection, &storedSource); err != nil {
		t.Fatal(err)
	}
	if storedDisposition != disposition {
		t.Fatalf("receipt %s disposition = %q, want %q", id, storedDisposition, disposition)
	}
	if value == "" && storedValue.Valid {
		t.Fatalf("receipt %s state_value_json = %q, want NULL", id, storedValue.String)
	}
	if value != "" && (!storedValue.Valid || storedValue.String != value) {
		t.Fatalf("receipt %s state_value_json = %#v, want %q", id, storedValue, value)
	}
	if rejection == "" && storedRejection.Valid {
		t.Fatalf("receipt %s rejection_code = %q, want NULL", id, storedRejection.String)
	}
	if rejection != "" && (!storedRejection.Valid || storedRejection.String != rejection) {
		t.Fatalf("receipt %s rejection_code = %#v, want %q", id, storedRejection, rejection)
	}
	if sourceUpdatedAt.IsZero() && storedSource.Valid {
		t.Fatalf("receipt %s source_updated_at = %q, want NULL", id, storedSource.String)
	}
	if !sourceUpdatedAt.IsZero() &&
		(!storedSource.Valid || storedSource.String != formatTime(sourceUpdatedAt)) {
		t.Fatalf("receipt %s source_updated_at = %#v, want %q", id, storedSource, formatTime(sourceUpdatedAt))
	}
}

func TestObservationProjectionStoresNormalizedStateValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewSQLiteRepository(database, catalog), nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	// This test protects normalized-value persistence and fails if the raw
	// Observation Value is stored instead of the canonical normalized form.
	padded := newObservation(t, entityID, `true`, observedAt)
	padded.Value = Value("  true \n")
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, padded, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionApplied || result.State == nil || string(result.State.Value) != "true" {
		t.Fatalf("whitespace-padded projection = %#v", result)
	}
	assertReceiptRow(t, database, padded.ID, "applied", `true`, "", time.Time{})

	canonical := newObservation(t, entityID, `true`, observedAt.Add(time.Second))
	second, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, canonical, observedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != DispositionUnchanged || second.State == nil ||
		string(second.State.Value) != "true" {
		t.Fatalf("canonical repeat projection = %#v", second)
	}
	assertReceiptRow(t, database, canonical.ID, "unchanged", `true`, "", time.Time{})

	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || string(view.State.Value) != "true" {
		t.Fatalf("entity view = %#v", view.State)
	}
}

func TestObservationProjectionRollsBackReceiptWhenStateWriteFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewSQLiteRepository(database, catalog)
	service := newTestService(repository, nil, catalog, Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	baseline := newObservation(t, entityID, `false`, observedAt)
	if _, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, baseline, observedAt); err != nil {
		t.Fatal(err)
	}
	command := newCommandRecord(t, entityID, observedAt.Add(time.Second))
	if _, err = repository.CreateCommand(ctx, command); err != nil {
		t.Fatal(err)
	}

	// This test protects receipt/state/command atomicity and fails if the
	// receipt insert commits without its State upsert and Command outcome.
	// The triggers force the State write to fail after the receipt insert
	// has run inside the same projection transaction.
	for _, trigger := range []string{
		`CREATE TRIGGER force_state_insert_failure BEFORE INSERT ON entity_states
			BEGIN SELECT RAISE(ABORT, 'forced state write failure'); END`,
		`CREATE TRIGGER force_state_update_failure BEFORE UPDATE ON entity_states
			BEGIN SELECT RAISE(ABORT, 'forced state write failure'); END`,
	} {
		if _, err = database.ExecContext(ctx, trigger); err != nil {
			t.Fatal(err)
		}
	}

	failing := newObservation(t, entityID, `true`, observedAt.Add(2*time.Second))
	failing.RefreshForCommand = &command.ID
	if _, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, failing, observedAt.Add(2*time.Second),
	); err == nil || !strings.Contains(err.Error(), "forced state write failure") {
		t.Fatalf("forced projection error = %v", err)
	}

	var receipts int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM observation_receipts WHERE observation_id = ?`, failing.ID,
	).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("aborted projection left %d receipts for %q", receipts, failing.ID)
	}
	assertReceiptCount(t, database, 1)

	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != baseline.ID || string(view.State.Value) != "false" {
		t.Fatalf("state after aborted projection = %#v", view.State)
	}
	stored, err := repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != CommandStatusRequested || stored.CompletedAt != nil ||
		stored.OutcomeObservationID != nil {
		t.Fatalf("command after aborted projection = %#v", stored)
	}
}
