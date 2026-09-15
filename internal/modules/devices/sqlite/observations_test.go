package sqlite //nolint:testpackage // Tests exercise package-private SQLite persistence behavior.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestObservationProjectionAdvancesStateByReceiveOrderAndDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
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
	if result.Disposition != devices.DispositionApplied || result.State == nil || string(result.State.Value) != "true" {
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
	if result.Disposition != devices.DispositionUnchanged || result.State == nil ||
		result.State.ReceiveOrder <= 1 || result.State.ObservationID != second.ID ||
		!result.State.AdapterReceivedAt.Equal(second.AdapterReceivedAt) {
		t.Fatalf("same-value projection = %#v", result)
	}

	redelivery := second
	redelivery.Value = devices.Value(`false`)
	result, err = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, redelivery, observedAt.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionDuplicate || result.State != nil {
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
	assertObservationCount(t, database, 2)

	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	database = openRegistrationDatabase(t, path)
	restarted := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
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
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	unknownEntityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		adapterID   string
		observation devices.Observation
		want        devices.ObservationRejection
	}{
		{
			"unknown entity", "simulator", newObservation(t, unknownEntityID, `true`, observedAt),
			devices.RejectionUnknownEntity,
		},
		{
			"wrong adapter", "homeassistant", newObservation(t, entityID, `true`, observedAt),
			devices.RejectionWrongAdapter,
		},
		{"invalid value", "simulator", newObservation(t, entityID, `1`, observedAt), devices.RejectionInvalidValue},
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
		if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
			*result.Rejection != test.want ||
			result.State != nil {
			t.Fatalf("%s: projection = %#v", test.name, result)
		}
	}
	assertObservationCount(t, database, len(tests))
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != nil {
		t.Fatalf("rejected observation created state: %#v", view.State)
	}
}

func statelessEffectRegistration() devices.Registration {
	registration := validDomainRegistration()
	registration.Entities = append(registration.Entities, devices.EntityDescriptor{
		Key: "effect", ExternalID: "light.office.effect", Name: "Effect", TypeID: devices.EntityTypeEnumactionV1,
		Support: devices.EntitySupport(`{"state":{},"operations":{"trigger":{"values":["blink","stop_effect"]}}}`),
	})
	return registration
}

func TestStatelessEntityRejectsEveryObservationWithoutStoringValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, statelessEffectRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[1].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	// Even the empty state object, null, and member values carry no reportable
	// state for a stateless action: every observation is invalid_value.
	for index, value := range []string{`{}`, `null`, `{"unexpected":true}`, `"blink"`} {
		result, projectionErr := service.ProjectObservation(
			ctx, "simulator", testRuntimeID, newObservation(t, entityID, value, observedAt),
			observedAt.Add(time.Duration(index)*time.Second),
		)
		if projectionErr != nil {
			t.Fatalf("value %s: %v", value, projectionErr)
		}
		if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
			*result.Rejection != devices.RejectionInvalidValue || result.State != nil {
			t.Fatalf("value %s: projection = %#v", value, result)
		}
	}
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != nil {
		t.Fatalf("rejected stateless observations created state: %#v", view.State)
	}

	// The rejections are visible in history reads with no stored value,
	// distinct from state history; the entity itself still reports null state.
	page, err := service.ListEntityStateHistory(ctx, devices.ListEntityStateHistoryParams{
		EntityID: entityID, Filter: devices.EntityStateHistoryFilterRejected, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("rejected history = %#v", page.Items)
	}
	for _, entry := range page.Items {
		if entry.Disposition != devices.DispositionRejected || entry.Rejection == nil ||
			*entry.Rejection != devices.RejectionInvalidValue || entry.Value != nil {
			t.Fatalf("rejected history entry = %#v", entry)
		}
	}
	updates, err := service.ListEntityStateHistory(ctx, devices.ListEntityStateHistoryParams{
		EntityID: entityID, Filter: devices.EntityStateHistoryFilterUpdates, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates.Items) != 0 {
		t.Fatalf("state-updates history = %#v, want none", updates.Items)
	}
}

func TestStatelessRejectionFollowsIdentityAndEnablementPrecedence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, statelessEffectRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[1].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	unknownEntityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}

	// Higher-precedence identity checks win over the stateless rejection.
	precedence := []struct {
		name      string
		adapterID string
		runtimeID devices.RuntimeID
		entityID  devices.EntityID
		want      devices.ObservationRejection
	}{
		{"unknown entity", "simulator", testRuntimeID, unknownEntityID, devices.RejectionUnknownEntity},
		{
			"wrong adapter", "homeassistant", testAdapterRuntime("homeassistant"),
			entityID, devices.RejectionWrongAdapter,
		},
		{
			"stale runtime", "simulator", devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-ffff00000000"),
			entityID, devices.RejectionStaleRuntime,
		},
	}
	for index, test := range precedence {
		result, projectionErr := service.ProjectObservation(
			ctx, test.adapterID, test.runtimeID,
			newObservation(t, test.entityID, `{}`, observedAt), observedAt.Add(time.Duration(index)*time.Second),
		)
		if projectionErr != nil {
			t.Fatalf("%s: %v", test.name, projectionErr)
		}
		if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
			*result.Rejection != test.want {
			t.Fatalf("%s: projection = %#v", test.name, result)
		}
	}

	// A disabled stateless entity without a linked command reports
	// entity_disabled, preserving the disabled-before-stateless order.
	_, enablementErr := service.SetEntityEnabled(ctx, entityID, false)
	if enablementErr != nil {
		t.Fatal(enablementErr)
	}
	result, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, newObservation(t, entityID, `{}`, observedAt), observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionEntityDisabled {
		t.Fatalf("disabled stateless projection = %#v", result)
	}
	_, reenableErr := service.SetEntityEnabled(ctx, entityID, true)
	if reenableErr != nil {
		t.Fatal(reenableErr)
	}

	// A linked active command does not exempt the stateless rejection: the
	// observation still carries no reportable state and satisfies nothing.
	linked := newCommandRecord(t, entityID, observedAt)
	if _, createErr := repository.CreateCommand(ctx, linked); createErr != nil {
		t.Fatal(createErr)
	}
	refresh := newObservation(t, entityID, `{}`, observedAt)
	refresh.RefreshForCommand = &linked.ID
	result, err = service.ProjectObservation(ctx, "simulator", testRuntimeID, refresh, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionInvalidValue || result.SatisfiedCommand != nil {
		t.Fatalf("linked stateless projection = %#v", result)
	}
}

func TestDisabledEntityRejectsUnlinkedObservationAndAllowsActiveCommandRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
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
	active.Parameters = devices.CommandParameters(`{"value":false}`)
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
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionEntityDisabled || result.State != nil {
		t.Fatalf("disabled projection = %#v", result)
	}
	duplicate, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, now.Add(time.Second),
	)
	if err != nil || duplicate.Disposition != devices.DispositionDuplicate {
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
	if result.Disposition != devices.DispositionApplied || result.State == nil || result.SatisfiedCommand == nil ||
		result.SatisfiedCommand.CommandID != active.ID {
		t.Fatalf("active refresh projection = %#v", result)
	}
	stored, err := repository.GetCommand(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != devices.CommandStatusSatisfied || stored.OutcomeObservationID == nil ||
		*stored.OutcomeObservationID != refresh.ID {
		t.Fatalf("active Command = %#v", stored)
	}
}

func TestDisabledEntityDoesNotExemptUnknownExpiredOrTerminalCommandLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	now := time.Date(2026, 8, 26, 12, 0, 20, 0, time.UTC)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
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
	if completionErr := repository.CompleteCommand(ctx, devices.CommandCompletion{
		ID: terminal.ID, Status: devices.CommandStatusOutcomeTimeout, CompletedAt: now,
		FailureCode: devices.CommandFailureOutcomeTimeout,
	}); completionErr != nil {
		t.Fatal(completionErr)
	}
	if _, enablementErr := service.SetEntityEnabled(ctx, entityID, false); enablementErr != nil {
		t.Fatal(enablementErr)
	}
	unknown, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []devices.CommandID{unknown, expired.ID, terminal.ID} {
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
		if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
			*result.Rejection != devices.RejectionEntityDisabled ||
			result.State != nil || result.SatisfiedCommand != nil {
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
	repository := NewDeviceRepository(database, catalog)
	completedAt := time.Date(2026, 8, 22, 12, 0, 2, 0, time.UTC)
	now := completedAt
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
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
	if stored.Status != devices.CommandStatusRequested {
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
		result.SatisfiedCommand.Outcome != devices.OutcomeObserved || result.SatisfiedCommand.ObservationID == nil ||
		*result.SatisfiedCommand.ObservationID != matching.ID || result.SatisfiedCommand.Value == nil ||
		string(*result.SatisfiedCommand.Value) != "true" {
		t.Fatalf("matching projection = %#v", result)
	}
	stored, err = repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != devices.CommandStatusSatisfied || stored.CompletedAt == nil ||
		!stored.CompletedAt.Equal(completedAt) ||
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
	if stored.Status != devices.CommandStatusRequested || stored.CompletedAt != nil ||
		stored.OutcomeObservationID != nil {
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
	if stored.Status != devices.CommandStatusInterrupted {
		t.Fatalf("terminal command status = %q", stored.Status)
	}
}

//nolint:gocognit,gocyclo,cyclop // One timeline verifies observation, deduplication, State, and Command effects.
func TestObservationRuntimeFencingRecordsStaleObservationAndIsolatesCommands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
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
	if releaseErr := repository.ReleaseAdapterRuntime(ctx, devices.ReleaseRuntimeWrite{
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
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionStaleRuntime || result.State != nil || result.SatisfiedCommand != nil {
		t.Fatalf("stale projection = %#v", result)
	}
	var observationRuntime, rejectionCode string
	if scanErr := database.QueryRowContext(ctx, `
		SELECT runtime_id, rejection_code
		FROM observations
		WHERE observation_id = ?`, stale.ID,
	).Scan(&observationRuntime, &rejectionCode); scanErr != nil {
		t.Fatal(scanErr)
	}
	if observationRuntime != string(testRuntimeID) || rejectionCode != string(devices.RejectionStaleRuntime) {
		t.Fatalf("stale observation = %q/%q", observationRuntime, rejectionCode)
	}

	unknownRuntime := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ae")
	unknownRuntimeObservation := newObservation(t, entityID, `false`, now)
	unknownResult, err := service.ProjectObservation(
		ctx, "simulator", unknownRuntime, unknownRuntimeObservation, now,
	)
	if err != nil || unknownResult.Disposition != devices.DispositionRejected || unknownResult.Rejection == nil ||
		*unknownResult.Rejection != devices.RejectionStaleRuntime {
		t.Fatalf("unknown-runtime projection = %#v, %v", unknownResult, err)
	}
	var unknownObservationRuntime sql.NullString
	if scanErr := database.QueryRowContext(ctx, `
		SELECT runtime_id FROM observations WHERE observation_id = ?`, unknownRuntimeObservation.ID,
	).Scan(&unknownObservationRuntime); scanErr != nil {
		t.Fatal(scanErr)
	}
	if unknownObservationRuntime.Valid {
		t.Fatalf("unknown-runtime observation retained invalid foreign key %q", unknownObservationRuntime.String)
	}

	duplicate, err := service.ProjectObservation(ctx, "simulator", testSecondRuntime, stale, now.Add(time.Second))
	if err != nil || duplicate.Disposition != devices.DispositionDuplicate || duplicate.State != nil {
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
	if result.Disposition != devices.DispositionApplied || result.State == nil ||
		result.State.ObservationID != fresh.ID || result.SatisfiedCommand != nil {
		t.Fatalf("replacement projection = %#v", result)
	}
	stored, err := repository.GetCommand(ctx, command.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != devices.CommandStatusRequested || stored.OutcomeObservationID != nil {
		t.Fatalf("old-runtime Command changed = %#v", stored)
	}
}

func TestObservationPruningPinsCurrentStateUntilItAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
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

	cutoff := start.Add(2 * time.Hour)
	if pruneErr := service.DeleteExpiredObservations(ctx, cutoff, time.Hour); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, []devices.ObservationID{second.ID})

	third := newObservation(t, entityID, `false`, cutoff)
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, third, cutoff,
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if pruneErr := service.DeleteExpiredObservations(
		ctx, cutoff.Add(time.Second), time.Hour,
	); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, []devices.ObservationID{third.ID})
}

// This test protects the exclusive retention cutoff keyed on Core observed
// time across applied, unchanged, and rejected dispositions. It fails if the
// comparison becomes inclusive or reads Adapter or source timestamps instead.
func TestObservationPruningUsesCoreObservedTimeExclusively(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	cutoff := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)
	oldSource := cutoff.Add(-30 * 24 * time.Hour)

	project := func(
		value string,
		observedAt, adapterReceivedAt time.Time,
		source *time.Time,
		want devices.ObservationDisposition,
	) devices.Observation {
		t.Helper()
		observation := newObservation(t, entityID, value, adapterReceivedAt)
		observation.SourceUpdatedAt = source
		result, projectionErr := service.ProjectObservation(
			ctx, "simulator", testRuntimeID, observation, observedAt,
		)
		if projectionErr != nil {
			t.Fatal(projectionErr)
		}
		if result.Disposition != want {
			t.Fatalf("project %s at %s disposition = %s, want %s", value, observedAt, result.Disposition, want)
		}
		return observation
	}

	// A newer Adapter timestamp does not protect an old observation.
	project(`false`, cutoff.Add(-2*time.Hour), cutoff.Add(time.Hour), nil, devices.DispositionApplied)
	// Repeating the current value is unchanged through the same projection
	// path, and the newer Adapter timestamp still does not protect it.
	project(`false`, cutoff.Add(-time.Nanosecond), cutoff.Add(time.Hour), nil, devices.DispositionUnchanged)
	// Old Adapter and source timestamps do not condemn observations exactly
	// on the cutoff; the boundary itself is retained.
	edgeAdapter := project(
		`true`, cutoff, cutoff.Add(-30*24*time.Hour), nil, devices.DispositionApplied,
	)
	edgeUnchanged := project(`true`, cutoff, cutoff, &oldSource, devices.DispositionUnchanged)
	// Rejected observations follow the same observed_at rule.
	project(`1`, cutoff.Add(-time.Nanosecond), cutoff, nil, devices.DispositionRejected)
	rejectedEdge := project(`1`, cutoff, cutoff, nil, devices.DispositionRejected)
	// The anchor row is newer than the cutoff either way.
	anchor := project(`false`, cutoff.Add(time.Hour), cutoff.Add(time.Hour), nil, devices.DispositionApplied)

	if pruneErr := service.DeleteExpiredObservations(
		ctx, cutoff.Add(time.Hour), time.Hour,
	); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(
		t, database,
		[]devices.ObservationID{edgeAdapter.ID, edgeUnchanged.ID, rejectedEdge.ID, anchor.ID},
	)
}

// This test protects retention policy changes applying to already persisted
// rows and fails if prune results depend on values stored at insert time.
func TestObservationPruningAppliesChangedPolicyToPersistedData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var ids []devices.ObservationID
	for index, value := range []string{`false`, `true`, `false`} {
		observation := newObservation(t, entityID, value, base.Add(time.Duration(index)*time.Hour))
		if _, projectionErr := service.ProjectObservation(
			ctx, "simulator", testRuntimeID, observation, base.Add(time.Duration(index)*time.Hour),
		); projectionErr != nil {
			t.Fatal(projectionErr)
		}
		ids = append(ids, observation.ID)
	}

	now := base.Add(3 * time.Hour)
	if pruneErr := service.DeleteExpiredObservations(ctx, now, 2*time.Hour); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, ids[1:])

	// Increasing the window preserves the surviving rows without resurrecting
	// the already deleted observation; the current-State anchor survives regardless.
	if pruneErr := service.DeleteExpiredObservations(ctx, now, 10*time.Hour); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, ids[1:])

	// Shortening the window prunes the same persisted rows further without
	// rewriting them; the current-State anchor survives regardless.
	if pruneErr := service.DeleteExpiredObservations(ctx, now, 30*time.Minute); pruneErr != nil {
		t.Fatal(pruneErr)
	}
	assertObservationIDs(t, database, ids[2:])
}

func TestObservationPruningRejectsMissingTimeAndRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := service.DeleteExpiredObservations(ctx, time.Time{}, time.Hour); err == nil {
		t.Fatal("prune without a prune time unexpectedly succeeded")
	}
	if err := service.DeleteExpiredObservations(ctx, now, 0); err == nil {
		t.Fatal("prune without a retention unexpectedly succeeded")
	}
}

// observationTestCorrelationID is the wire correlation every Observation
// report in these tests carries; an accepted Observation fact copies it.
const observationTestCorrelationID = devices.CorrelationID("cor_01890f47-7a6b-7c4d-8e9f-0123456789ab")

func newObservation(
	t *testing.T, entityID devices.EntityID, value string, adapterReceivedAt time.Time,
) devices.Observation {
	t.Helper()
	id, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.Observation{
		ID: id, EntityID: entityID, Value: devices.Value(value),
		CorrelationID: observationTestCorrelationID, AdapterReceivedAt: adapterReceivedAt,
	}
}

func assertObservationCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow("SELECT count(*) FROM observations").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("observation count = %d, want %d", got, want)
	}
}

func assertObservationIDs(t *testing.T, database *sql.DB, want []devices.ObservationID) {
	t.Helper()
	rows, err := database.Query("SELECT observation_id FROM observations ORDER BY receive_order")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []devices.ObservationID
	for rows.Next() {
		var id devices.ObservationID
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		got = append(got, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if len(got) != len(want) {
		t.Fatalf("observation IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("observation IDs = %v, want %v", got, want)
		}
	}
}

func resultReceiveOrder(t *testing.T, database *sql.DB, id devices.ObservationID) int64 {
	t.Helper()
	var order int64
	if err := database.QueryRow("SELECT receive_order FROM observations WHERE observation_id = ?", id).
		Scan(&order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestObservationProjectionEnrichesObservationsWithNormalizedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
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
	assertObservationRow(t, database, applied.ID, "applied", `true`, "", sourceUpdatedAt)

	unchanged := newObservation(t, entityID, `true`, observedAt.Add(time.Second))
	if _, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, unchanged, observedAt.Add(time.Second),
	); projectionErr != nil {
		t.Fatal(projectionErr)
	}
	assertObservationRow(t, database, unchanged.ID, "unchanged", `true`, "", time.Time{})

	rejected := newObservation(t, entityID, `1`, observedAt.Add(2*time.Second))
	rejected.SourceUpdatedAt = &sourceUpdatedAt
	result, projectionErr := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, rejected, observedAt.Add(2*time.Second),
	)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionInvalidValue {
		t.Fatalf("invalid-value projection = %#v", result)
	}
	assertObservationRow(
		t, database, rejected.ID, "rejected", "",
		string(devices.RejectionInvalidValue), sourceUpdatedAt,
	)

	unknownEntityID, err := devices.NewEntityID()
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
	if result.Disposition != devices.DispositionRejected || result.Rejection == nil ||
		*result.Rejection != devices.RejectionUnknownEntity {
		t.Fatalf("unknown-entity projection = %#v", result)
	}
	assertObservationRow(
		t, database, unknown.ID, "rejected", "",
		string(devices.RejectionUnknownEntity), sourceUpdatedAt,
	)

	redelivery := unchanged
	redelivery.Value = devices.Value(`false`)
	redelivery.SourceUpdatedAt = &sourceUpdatedAt
	result, projectionErr = service.ProjectObservation(
		ctx, "simulator", testRuntimeID, redelivery, observedAt.Add(4*time.Second),
	)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	if result.Disposition != devices.DispositionDuplicate {
		t.Fatalf("duplicate projection = %#v", result)
	}
	assertObservationCount(t, database, 4)
}

func assertObservationRow(
	t *testing.T,
	database *sql.DB,
	id devices.ObservationID,
	disposition, value, rejection string,
	sourceUpdatedAt time.Time,
) {
	t.Helper()
	var storedDisposition string
	var storedValue, storedRejection, storedSource sql.NullString
	if err := database.QueryRow(`
		SELECT disposition, state_value_json, rejection_code, source_updated_at
		FROM observations
		WHERE observation_id = ?`, id,
	).Scan(&storedDisposition, &storedValue, &storedRejection, &storedSource); err != nil {
		t.Fatal(err)
	}
	if storedDisposition != disposition {
		t.Fatalf("observation %s disposition = %q, want %q", id, storedDisposition, disposition)
	}
	if value == "" && storedValue.Valid {
		t.Fatalf("observation %s state_value_json = %q, want NULL", id, storedValue.String)
	}
	if value != "" && (!storedValue.Valid || storedValue.String != value) {
		t.Fatalf("observation %s state_value_json = %#v, want %q", id, storedValue, value)
	}
	if rejection == "" && storedRejection.Valid {
		t.Fatalf("observation %s rejection_code = %q, want NULL", id, storedRejection.String)
	}
	if rejection != "" && (!storedRejection.Valid || storedRejection.String != rejection) {
		t.Fatalf("observation %s rejection_code = %#v, want %q", id, storedRejection, rejection)
	}
	if sourceUpdatedAt.IsZero() && storedSource.Valid {
		t.Fatalf("observation %s source_updated_at = %q, want NULL", id, storedSource.String)
	}
	if !sourceUpdatedAt.IsZero() &&
		(!storedSource.Valid || storedSource.String != formatTime(sourceUpdatedAt)) {
		t.Fatalf("observation %s source_updated_at = %#v, want %q", id, storedSource, formatTime(sourceUpdatedAt))
	}
}

func TestObservationProjectionStoresNormalizedStateValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	service := newTestService(NewDeviceRepository(database, catalog), nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	// This test protects normalized-value persistence and fails if the raw
	// Observation Value is stored instead of the canonical normalized form.
	padded := newObservation(t, entityID, `true`, observedAt)
	padded.Value = devices.Value("  true \n")
	result, err := service.ProjectObservation(ctx, "simulator", testRuntimeID, padded, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionApplied || result.State == nil || string(result.State.Value) != "true" {
		t.Fatalf("whitespace-padded projection = %#v", result)
	}
	assertObservationRow(t, database, padded.ID, "applied", `true`, "", time.Time{})

	canonical := newObservation(t, entityID, `true`, observedAt.Add(time.Second))
	second, err := service.ProjectObservation(
		ctx, "simulator", testRuntimeID, canonical, observedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != devices.DispositionUnchanged || second.State == nil ||
		string(second.State.Value) != "true" {
		t.Fatalf("canonical repeat projection = %#v", second)
	}
	assertObservationRow(t, database, canonical.ID, "unchanged", `true`, "", time.Time{})

	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || string(view.State.Value) != "true" {
		t.Fatalf("entity view = %#v", view.State)
	}
}

func TestObservationProjectionRollsBackObservationWhenStateWriteFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{})
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

	// This test protects observation/state/command atomicity and fails if the
	// observation insert commits without its State upsert and Command outcome.
	// The triggers force the State write to fail after the observation insert
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

	var observationRowCount int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM observations WHERE observation_id = ?`, failing.ID,
	).Scan(&observationRowCount); err != nil {
		t.Fatal(err)
	}
	if observationRowCount != 0 {
		t.Fatalf("aborted projection left %d observations for %q", observationRowCount, failing.ID)
	}
	assertObservationCount(t, database, 1)

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
	if stored.Status != devices.CommandStatusRequested || stored.CompletedAt != nil ||
		stored.OutcomeObservationID != nil {
		t.Fatalf("command after aborted projection = %#v", stored)
	}
}
