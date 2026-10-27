package automations

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestValueLeavesAreTraversedWithoutChildSequences(t *testing.T) {
	snapshot := Definition{
		Name: "Data leaves", Triggers: []Trigger{{ID: "clock", Body: CronTrigger{Expression: "0 12 * * *"}}},
		Steps: []Step{
			{ID: "read", Body: ReadStateStep{EntityID: devices.EntityID("ent_01900000-0000-7000-8000-000000000001")}},
			{ID: "assign", Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{
				"copy": {Body: ExpressionValue{Source: "steps.read.output"}},
			}}},
		},
	}
	prepared, err := NormalizeDefinition(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []StepID{"read", "assign"} {
		step, lookupErr := StepWithPreparedSnapshot(prepared, id)
		if lookupErr != nil || step.ID != id || len(snapshotChildSequences(step)) != 0 {
			t.Fatalf("leaf %s: %v", id, lookupErr)
		}
	}
}

func TestRecordedCommandStoppedBeforeReservationHasNoInputs(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	snapshot := Definition{Steps: []Step{{ID: "command", Body: CommandStep{
		EntityID: devices.EntityID("ent_01900000-0000-7000-8000-000000000001"), OperationName: "set",
		Parameters: ValueBinding{Body: LiteralValue{Value: json.RawMessage(`{"value":true}`)}},
	}}}}
	details, _, err := PrepareStepExecutionDetails(StepExecutionRecord{
		RunID: "arn_01900000-0000-7000-8000-000000000001", StepID: "command",
		Evidence: CommandStopped{At: at, FailureCode: FailureCoreStopping},
	})
	if err != nil {
		t.Fatal(err)
	}
	execution := StepExecution{StepID: "command", Details: details}
	if err = ValidateStepValueEvidenceWithPreparedSnapshot(execution, snapshot, true); err != nil {
		t.Fatalf("pre-reservation interruption required fabricated inputs: %v", err)
	}
	details = CommandExecutionDetails{StartedAt: at, State: CommandInterrupted{
		CompletedAt: at, FailureCode: FailureCoreStopping,
		Reservation: &CommandReservation{
			CommandID:     "cmd_01900000-0000-7000-8000-000000000001",
			CorrelationID: "cor_01900000-0000-7000-8000-000000000001",
		},
	}}
	execution.Details = details
	if err = ValidateStepValueEvidenceWithPreparedSnapshot(execution, snapshot, true); err == nil {
		t.Fatal("reserved recorded Command accepted missing inputs")
	}
}
