package api //nolint:testpackage // Tests protect the public mapping's reservation privacy boundary.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestStepAttemptMappingNeverPublishesReservation(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	completed := started.Add(time.Second)
	verified := devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	reservation := automations.CommandReservation{
		CommandID:     "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
	}
	for _, test := range []struct {
		name     string
		state    automations.StepAttemptState
		status   string
		verified bool
		failure  string
	}{
		{name: "not attempted", state: automations.NotAttemptedStep{}, status: "not_attempted"},
		{name: "running", state: automations.RunningStep{StartedAt: started, Reservation: reservation}, status: "running"},
		{name: "satisfied", state: automations.CompletedStep{StartedAt: started, CompletedAt: completed, Reservation: &reservation, Outcome: automations.SatisfiedStep{VerifiedCommandID: verified}}, status: "satisfied", verified: true},
		{name: "dispatched", state: automations.CompletedStep{StartedAt: started, CompletedAt: completed, Reservation: &reservation, Outcome: automations.DispatchedStep{VerifiedCommandID: verified}}, status: "dispatched", verified: true},
		{name: "failed", state: automations.CompletedStep{StartedAt: started, CompletedAt: completed, Reservation: &reservation, Outcome: automations.FailedStep{FailureCode: "failed", VerifiedCommandID: &verified}}, status: "failed", verified: true, failure: "failed"},
		{name: "interrupted with unverified reservation", state: automations.CompletedStep{StartedAt: started, CompletedAt: completed, Reservation: &reservation, Outcome: automations.InterruptedStep{FailureCode: "core_stopping"}}, status: "interrupted", failure: "core_stopping"},
		{name: "interrupted before reservation", state: automations.CompletedStep{StartedAt: started, CompletedAt: completed, Outcome: automations.InterruptedStep{FailureCode: "core_stopping"}}, status: "interrupted", failure: "core_stopping"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := automationStepAttemptBody(
				automations.StepAttempt{Position: 0, StepID: "command", State: test.state},
			)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Status            string     `json:"status"`
				VerifiedCommandID *string    `json:"verified_command_id"`
				FailureCode       *string    `json:"failure_code"`
				StartedAt         *time.Time `json:"started_at"`
				CompletedAt       *time.Time `json:"completed_at"`
			}
			if err = json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Status != test.status || (wire.VerifiedCommandID != nil) != test.verified {
				t.Fatalf("public Step = %s", raw)
			}
			if test.verified && *wire.VerifiedCommandID != string(verified) {
				t.Fatalf("verified Command = %s, want outcome evidence %s", *wire.VerifiedCommandID, verified)
			}
			assertPublicStepTimestamps(t, test.status, wire.StartedAt, wire.CompletedAt, started, completed)
			assertPublicStepFailure(t, wire.FailureCode, test.failure)
			assertPublicStepPrivacy(t, body)
		})
	}
}

func assertPublicStepFailure(t *testing.T, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("failure = %q, want absent", *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Fatalf("failure = %v, want %q", got, want)
	}
}

func assertPublicStepTimestamps(t *testing.T, status string, start, end *time.Time, started, completed time.Time) {
	t.Helper()
	if status == "not_attempted" {
		if start != nil || end != nil {
			t.Fatalf("unattempted Step has timestamps: start %v, end %v", start, end)
		}
		return
	}
	if start == nil || !start.Equal(started) {
		t.Fatalf("Step start = %v, want %v", start, started)
	}
	if status == "running" {
		if end != nil {
			t.Fatalf("running Step has completion: %v", end)
		}
		return
	}
	if end == nil || !end.Equal(completed) {
		t.Fatalf("Step completion = %v, want %v", end, completed)
	}
}

func assertPublicStepPrivacy(t *testing.T, body AutomationStepAttemptBody) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"reservation", "reserved_command_id", "reserved_correlation_id", "correlation_id", "state"} {
		if _, present := fields[private]; present {
			t.Errorf("public Step leaked %s: %s", private, encoded)
		}
	}
}
