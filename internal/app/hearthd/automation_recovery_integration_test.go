package hearthd //nolint:testpackage // Recovery fixtures exercise app startup against SQLite and embedded NATS.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type automationRecoveryExpectation struct {
	id            automations.AutomationRunID
	commandStatus devices.CommandStatus
	pending       bool
}

// The actual process startup test invokes these fixtures with its embedded NATS
// observer, covering crash windows without launching any executable work.
func seedAutomationRecoveryRuns(
	t *testing.T,
	database *sql.DB,
	commands *devices.SQLiteRepository,
	requested, accepted devices.CommandRecord,
) []automationRecoveryExpectation {
	t.Helper()
	repo := automations.NewSQLiteRepository(database)
	missing := recoveryCommandRecord(t, requested.EntityID, time.Now().UTC())
	completed := recoveryCommandRecord(t, requested.EntityID, time.Now().UTC())
	collision := recoveryCommandRecord(t, requested.EntityID, time.Now().UTC())
	for _, command := range []devices.CommandRecord{completed, collision} {
		if _, err := commands.CreateCommand(t.Context(), command); err != nil {
			t.Fatal(err)
		}
		if err := commands.CompleteCommand(
			t.Context(),
			devices.CommandCompletion{
				ID:          command.ID,
				Status:      devices.CommandStatusDispatched,
				CompletedAt: time.Now().UTC(),
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	marker, markerErr := devices.NewCorrelationID()
	if markerErr != nil {
		t.Fatal(markerErr)
	}
	collision.CorrelationID = marker // fresh marker, not copied from the old Command
	cases := []struct {
		name    string
		command devices.CommandRecord
		want    devices.CommandStatus
		pending bool
	}{
		{name: "before-intent", pending: true},
		{name: "before-command", command: missing},
		{name: "after-creation", command: requested, want: devices.CommandStatusInterrupted},
		{name: "after-acceptance", command: accepted, want: devices.CommandStatusInterrupted},
		{name: "after-completion", command: completed, want: devices.CommandStatusDispatched},
		{name: "colliding-intent", command: collision},
	}
	expectations := make([]automationRecoveryExpectation, 0, len(cases))
	for _, test := range cases {
		definition := automations.AutomationDefinition{
			Name: test.name,
			Triggers: []automations.AutomationTrigger{
				{ID: "daily", Kind: automations.AutomationTriggerKindCron, Expression: "0 19 * * *"},
			},
			Steps: []automations.AutomationStep{
				{
					EntityID:      requested.EntityID,
					OperationName: "set",
					Parameters:    devices.CommandParameters(`{"value":true}`),
				},
				{
					EntityID:      requested.EntityID,
					OperationName: "set",
					Parameters:    devices.CommandParameters(`{"value":false}`),
				},
			},
		}
		automation, err := repo.CreateAutomation(t.Context(), definition)
		if err != nil {
			t.Fatal(err)
		}
		admission, err := repo.AdmitManualRun(
			t.Context(),
			automations.AutomationManualAdmission{
				Request:  automations.AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: test.name},
				Timezone: "UTC",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !test.pending {
			if err = repo.BeginAutomationStep(
				t.Context(),
				automations.AutomationStepStart{
					RunID:         admission.Run.ID,
					Index:         0,
					CommandID:     test.command.ID,
					CorrelationID: test.command.CorrelationID,
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		expectations = append(
			expectations,
			automationRecoveryExpectation{id: admission.Run.ID, commandStatus: test.want, pending: test.pending},
		)
	}
	return expectations
}

//nolint:gocognit // Crash-window expectations include distinct step and ownership invariants.
func verifyAutomationRecoveryRuns(
	t *testing.T,
	repo *automations.SQLiteRepository,
	expectations []automationRecoveryExpectation,
) {
	t.Helper()
	for _, expected := range expectations {
		run, err := repo.GetAutomationRun(t.Context(), expected.id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != automations.AutomationRunStatusInterrupted || run.FailureCode == nil ||
			*run.FailureCode != automations.AutomationFailureCoreRestarted ||
			run.CompletedAt == nil ||
			run.Steps[1].Status != automations.AutomationStepStatusNotAttempted {
			t.Fatalf("startup did not interrupt sequence: %#v", run)
		}
		want := automations.AutomationStepStatusInterrupted
		if expected.pending {
			want = automations.AutomationStepStatusNotAttempted
		}
		if run.Steps[0].Status != want {
			t.Fatalf("recovery first step = %#v", run.Steps[0])
		}
		if expected.commandStatus == "" {
			if run.Steps[0].CommandID != nil || run.Steps[0].CommandStatus != nil || run.Steps[0].Outcome != nil {
				t.Fatalf("recovery adopted missing/foreign command: %#v", run.Steps[0])
			}
		} else if run.Steps[0].CommandStatus == nil || *run.Steps[0].CommandStatus != expected.commandStatus || run.Steps[0].CommandID == nil {
			t.Fatalf("recovery lost owned command: %#v", run.Steps[0])
		}
		if expected.commandStatus == devices.CommandStatusDispatched &&
			(run.Steps[0].Outcome == nil || *run.Steps[0].Outcome != devices.OutcomeDispatched) {
			t.Fatalf("recovery lost dispatched outcome: %#v", run.Steps[0])
		}
	}
}
