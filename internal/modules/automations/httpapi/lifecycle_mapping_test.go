package httpapi_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestPublicCommandExecutionDistinguishesReservationFromVerification(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	completed := started.Add(time.Second)
	reservation := automations.CommandReservation{
		CommandID:     "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
	}
	verified := reservation.CommandID
	for _, test := range []struct {
		name     string
		details  automations.CommandExecutionDetails
		phase    string
		verified bool
		failure  string
	}{
		{name: "running reservation", details: automations.CommandExecutionDetails{StartedAt: started, Reservation: &reservation}, phase: "running"},
		{name: "satisfied", details: automations.CommandExecutionDetails{StartedAt: started, CompletedAt: &completed, Reservation: &reservation, Outcome: automations.SatisfiedCommand{VerifiedCommandID: verified}}, phase: "succeeded", verified: true},
		{name: "dispatched", details: automations.CommandExecutionDetails{StartedAt: started, CompletedAt: &completed, Reservation: &reservation, Outcome: automations.DispatchedCommand{VerifiedCommandID: verified}}, phase: "succeeded", verified: true},
		{name: "failed", details: automations.CommandExecutionDetails{StartedAt: started, CompletedAt: &completed, Reservation: &reservation, Outcome: automations.FailedCommand{FailureCode: "failed", VerifiedCommandID: &verified}}, phase: "failed", verified: true, failure: "failed"},
		{name: "interrupted after reservation", details: automations.CommandExecutionDetails{StartedAt: started, CompletedAt: &completed, Reservation: &reservation, Outcome: automations.InterruptedCommand{FailureCode: "core_stopping"}}, phase: "interrupted", failure: "core_stopping"},
		{name: "interrupted before reservation", details: automations.CommandExecutionDetails{StartedAt: started, CompletedAt: &completed, Outcome: automations.InterruptedCommand{FailureCode: "core_stopping"}}, phase: "interrupted", failure: "core_stopping"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := automations.EncodePublicStepExecution(automations.StepExecution{
				StepID: "command", Sequence: 0, Details: test.details,
			})
			if err != nil {
				t.Fatal(err)
			}
			evidence := assertCommandExecutionEnvelope(t, raw, test.phase, started, completed)
			assertCommandReservationEvidence(t, evidence, test.details.Reservation)
			if test.verified {
				assertCommandEvidenceValue(t, evidence, "verified_command_id", string(verified))
			} else {
				assertCommandEvidenceValue(t, evidence, "verified_command_id", "")
			}
			assertCommandEvidenceValue(t, evidence, "reason_code", test.failure)
		})
	}
}

func assertCommandExecutionEnvelope(
	t *testing.T,
	raw []byte,
	phase string,
	started, completed time.Time,
) map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		StepID      string                     `json:"step_id"`
		Sequence    int                        `json:"sequence"`
		Kind        string                     `json:"kind"`
		Phase       string                     `json:"phase"`
		StartedAt   time.Time                  `json:"started_at"`
		CompletedAt *time.Time                 `json:"completed_at"`
		Evidence    map[string]json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.StepID != "command" || wire.Sequence != 0 || wire.Kind != "command" || wire.Phase != phase ||
		!wire.StartedAt.Equal(started) {
		t.Fatalf("public execution envelope = %s", raw)
	}
	if (wire.CompletedAt != nil) != (phase != "running") ||
		wire.CompletedAt != nil && !wire.CompletedAt.Equal(completed) {
		t.Fatalf("public completion time = %v", wire.CompletedAt)
	}
	return wire.Evidence
}

func assertCommandReservationEvidence(
	t *testing.T,
	evidence map[string]json.RawMessage,
	reservation *automations.CommandReservation,
) {
	t.Helper()
	for _, private := range []string{"reservation", "reserved_command_id", "reserved_correlation_id", "state"} {
		if _, present := evidence[private]; present {
			t.Errorf("public execution leaked %s: %s", private, evidence[private])
		}
	}
	commandID, correlationID := "", ""
	if reservation != nil {
		commandID, correlationID = string(reservation.CommandID), string(reservation.CorrelationID)
	}
	assertCommandEvidenceValue(t, evidence, "command_id", commandID)
	assertCommandEvidenceValue(t, evidence, "correlation_id", correlationID)
}

func assertCommandEvidenceValue(t *testing.T, evidence map[string]json.RawMessage, key, want string) {
	t.Helper()
	if want == "" {
		if _, present := evidence[key]; present {
			t.Fatalf("invented %s: %s", key, evidence[key])
		}
		return
	}
	var got string
	if err := json.Unmarshal(evidence[key], &got); err != nil || got != want {
		t.Fatalf("evidence %s = %q, want %q: %v", key, got, want, err)
	}
}
