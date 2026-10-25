//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// A disabled document with an invalid body must not be decoded by automatic
// admission, but management reads must still report its validation error.
func TestAdmissionFiltersDisabledDefinitionsBeforeDecoding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-10-02T08:59:00Z")
	repository := scheduleRepo(database, &at)
	definition := validDomainDefinition(t)
	definition.Triggers = append(definition.Triggers, scheduleDefinition(t, "* * * * *").Triggers...)
	enabled, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Enabled = false
	disabled, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`UPDATE automations SET definition_json = json_remove(definition_json, '$.name') WHERE id = ?`,
		string(disabled.ID),
	)
	records, err := repository.ListEnabledAutomations(ctx)
	if err != nil || len(records) != 1 || records[0].ID != enabled.ID {
		t.Fatalf("enabled pre-read = %#v, %v", records, err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	scheduled := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if scheduled.Outcome.StartedRuns != 1 || scheduled.StartedRuns[0].AutomationID != enabled.ID {
		t.Fatalf("schedule admission = %#v", scheduled)
	}
	initializeStorageRun(t, repository, &scheduled.StartedRuns[0])
	if err = repository.CompleteRun(ctx, automations.RunCompletion{
		RunID: scheduled.StartedRuns[0].ID, Outcome: automations.SucceededRun{},
	}); err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, definition.Triggers[0].Body.(automations.ObservationTrigger).EntityID, at)
	admitted, err := repository.AdmitDeviceFact(ctx, fact, stateSnapshotWith(), at, at.Add(-time.Hour))
	if err != nil || admitted.Outcome.StartedRuns != 1 || admitted.StartedRuns[0].AutomationID != enabled.ID {
		t.Fatalf("Fact admission = %#v, %v", admitted, err)
	}
	if _, err = repository.GetAutomation(ctx, disabled.ID); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("disabled management get = %v", err)
	}
	if _, err = repository.ListAutomations(
		ctx,
		automations.ListAutomationsParams{},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("management list = %v", err)
	}
}

func TestEnabledAdmissionReadRejectsInvalidEnablement(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{}`, `{"enabled":null}`, `{"enabled":0}`, `{"enabled":"false"}`, `{"enabled":true}`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
				VALUES (?, 1, ?, ?, ?)`, newAutomationIDString(t), raw, migrationTimestamp, migrationTimestamp)
			if _, err := repository.ListEnabledAutomations(
				context.Background(),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("invalid enabled document = %v", err)
			}
		})
	}
}
