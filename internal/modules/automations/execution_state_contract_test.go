package automations_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// State changes must preserve the established evidence and both envelope timestamps.
func TestExecutionStateWireContract(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	completed := started.Add(-time.Second) // Wall-clock order is not execution order.
	const commandID devices.CommandID = "cmd_018f47c2-7b3d-7abc-8abc-123456789abc"
	const correlationID devices.CorrelationID = "cor_018f47c2-7b3d-7abc-8abc-123456789abc"
	const attemptID notifications.AttemptID = "ntf_018f47c2-7b3d-7abc-8abc-123456789abc"
	const invocationID adapters.InvocationID = "afi_018f47c2-7b3d-7abc-8abc-123456789abc"
	const catalogID adapters.CatalogID = "afc_018f47c2-7b3d-7abc-8abc-123456789abc"
	reservation := automations.CommandReservation{CommandID: commandID, CorrelationID: correlationID}
	const commandReservation = `"command_id":"cmd_018f47c2-7b3d-7abc-8abc-123456789abc","correlation_id":"cor_018f47c2-7b3d-7abc-8abc-123456789abc"`
	const verified = `"verified_command_id":"cmd_018f47c2-7b3d-7abc-8abc-123456789abc"`
	const attempt = `"attempt_id":"ntf_018f47c2-7b3d-7abc-8abc-123456789abc"`
	const invocation = `"invocation_id":"afi_018f47c2-7b3d-7abc-8abc-123456789abc"`
	const catalog = `"catalog_revision":"afc_018f47c2-7b3d-7abc-8abc-123456789abc"`
	for _, test := range []struct {
		name     string
		details  automations.StepExecutionDetails
		phase    string
		evidence string
	}{
		{"command running", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandRunning{Reservation: reservation}}, "running", `{` + commandReservation + `}`},
		{"command satisfied", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandSucceeded{CompletedAt: completed, Reservation: reservation, Outcome: automations.SatisfiedCommand{VerifiedCommandID: commandID}}}, "succeeded", `{` + commandReservation + `,` + verified + `,"command_outcome":"satisfied"}`},
		{"command dispatched", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandSucceeded{CompletedAt: completed, Reservation: reservation, Outcome: automations.DispatchedCommand{VerifiedCommandID: commandID}}}, "succeeded", `{` + commandReservation + `,` + verified + `,"command_outcome":"dispatched"}`},
		{"command failed", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandFailed{CompletedAt: completed, Reservation: reservation, FailureCode: "provider_error"}}, "failed", `{` + commandReservation + `,"reason_code":"provider_error"}`},
		{"command interrupted", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandInterrupted{CompletedAt: completed, Reservation: &reservation, VerifiedCommandID: new(commandID), FailureCode: "executor_fault"}}, "interrupted", `{` + commandReservation + `,` + verified + `,"reason_code":"executor_fault"}`},
		{"command unreserved", automations.CommandExecutionDetails{StartedAt: started, State: automations.CommandInterrupted{CompletedAt: completed, FailureCode: "core_stopping"}}, "interrupted", `{"reason_code":"core_stopping"}`},
		{"delay running", automations.DelayExecutionDetails{StartedAt: started, State: automations.DelayRunning{}}, "running", `{}`},
		{"delay succeeded", automations.DelayExecutionDetails{StartedAt: started, State: automations.DelaySucceeded{CompletedAt: completed}}, "succeeded", `{}`},
		{"delay interrupted", automations.DelayExecutionDetails{StartedAt: started, State: automations.DelayInterrupted{CompletedAt: completed, FailureCode: "core_restarted"}}, "interrupted", `{"reason_code":"core_restarted"}`},
		{"notification running", automations.NotificationExecutionDetails{StartedAt: started, AttemptID: attemptID, State: automations.NotificationRunning{}}, "running", `{` + attempt + `}`},
		{"notification accepted", automations.NotificationExecutionDetails{StartedAt: started, AttemptID: attemptID, State: automations.NotificationSucceeded{CompletedAt: completed}}, "succeeded", `{` + attempt + `,"outcome":"accepted"}`},
		{"notification admission rejected", automations.NotificationExecutionDetails{StartedAt: started, AttemptID: attemptID, State: automations.NotificationFailed{CompletedAt: completed, Evidence: automations.NotificationAdmissionRejected{ReasonCode: "destination_not_found"}}}, "failed", `{` + attempt + `,"reason_code":"destination_not_found"}`},
		{"notification attempt rejected", automations.NotificationExecutionDetails{StartedAt: started, AttemptID: attemptID, State: automations.NotificationFailed{CompletedAt: completed, Evidence: automations.NotificationAttemptFailed{Outcome: notifications.Rejected, ReasonCode: "provider_rejected"}}}, "failed", `{` + attempt + `,"outcome":"rejected","reason_code":"provider_rejected"}`},
		{"notification interrupted", automations.NotificationExecutionDetails{StartedAt: started, AttemptID: attemptID, State: automations.NotificationInterrupted{CompletedAt: completed, ReasonCode: "executor_fault"}}, "interrupted", `{` + attempt + `,"reason_code":"executor_fault"}`},
		{"function running", automations.FunctionExecutionDetails{StartedAt: started, InvocationID: invocationID, State: automations.FunctionRunning{}}, "running", `{` + invocation + `}`},
		{"function succeeded", automations.FunctionExecutionDetails{StartedAt: started, InvocationID: invocationID, State: automations.FunctionSucceeded{CompletedAt: completed, CatalogRevision: catalogID}}, "succeeded", `{` + invocation + `,` + catalog + `,"invocation_outcome":"succeeded"}`},
		{"function admission rejected", automations.FunctionExecutionDetails{StartedAt: started, InvocationID: invocationID, State: automations.FunctionFailed{CompletedAt: completed, Evidence: automations.FunctionAdmissionRejected{ReasonCode: "function_admission_closed"}}}, "failed", `{` + invocation + `,"reason_code":"function_admission_closed"}`},
		{"function invocation failed", automations.FunctionExecutionDetails{StartedAt: started, InvocationID: invocationID, State: automations.FunctionFailed{CompletedAt: completed, Evidence: automations.FunctionInvocationFailed{CatalogRevision: catalogID, Outcome: adapters.Unknown{ReasonCode: "outcome_timeout"}}}}, "failed", `{` + invocation + `,` + catalog + `,"invocation_outcome":"unknown","reason_code":"outcome_timeout"}`},
		{"function interrupted", automations.FunctionExecutionDetails{StartedAt: started, InvocationID: invocationID, State: automations.FunctionInterrupted{CompletedAt: completed, ReasonCode: "core_restarted"}}, "interrupted", `{` + invocation + `,"reason_code":"core_restarted"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertExecutionStateWireContract(t, test.details, test.phase, test.evidence, started, completed)
		})
	}
}

func assertExecutionStateWireContract(
	t *testing.T,
	details automations.StepExecutionDetails,
	phase, evidence string,
	started, completed time.Time,
) {
	t.Helper()
	envelope, err := automations.EncodeStepExecutionDetails(details)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Phase != phase || !envelope.StartedAt.Equal(started) {
		t.Fatalf("unexpected lifecycle: %+v", envelope)
	}
	if phase == "running" {
		if envelope.CompletedAt != nil {
			t.Fatal("running state invented completion time")
		}
	} else if envelope.CompletedAt == nil || !envelope.CompletedAt.Equal(completed) {
		t.Fatalf("completion time = %v, want %v", envelope.CompletedAt, completed)
	}
	var got, want any
	if err = json.Unmarshal(envelope.EvidenceJSON, &got); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(evidence), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evidence = %s, want %s", envelope.EvidenceJSON, evidence)
	}
	decoded, err := automations.DecodeStepExecutionDetails("step", envelope)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := automations.EncodeStepExecutionDetails(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envelope, reencoded) {
		t.Fatalf("round trip changed lifecycle/evidence: %+v -> %+v", envelope, reencoded)
	}
}
