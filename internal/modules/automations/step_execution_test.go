package automations_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	automations "github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestExecutionDetailsOwnValidation(t *testing.T) {
	t.Parallel()
	for _, test := range executionValidationCases() {
		// Nil and pointer representations are rejected by the common entry point,
		// not by methods on the supported concrete value variants.
		switch test.details.(type) {
		case automations.CommandExecutionDetails, automations.DelayExecutionDetails,
			automations.NotificationExecutionDetails, automations.FunctionExecutionDetails,
			automations.BranchExecutionDetails, automations.StateExecutionDetails,
			automations.ValueFailureExecutionDetails, automations.VariablesExecutionDetails:
		default:
			continue
		}
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.details.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate error = %v, want valid=%v", err, test.valid)
			}
			if err != nil && !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("Validate error = %v, want invalid Automation", err)
			}
		})
	}
}

func TestStepExecutionDelayCodecAndCompletion(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	details, env, err := automations.PrepareStepExecutionDetails(
		automations.StepExecutionRecord{
			RunID:    automations.RunID("arn_018f47c2-7b3d-7abc-8abc-123456789abc"),
			StepID:   "wait",
			Evidence: automations.DelayStarted{StartedAt: at},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != automations.StepKindDelay || env.Phase != "running" || env.CompletedAt != nil {
		t.Fatalf("envelope=%+v", env)
	}
	decoded, err := automations.DecodeStepExecutionDetails("wait", env)
	if err != nil {
		t.Fatal(err)
	}
	finished, completedEnv, err := automations.CompleteStepDetails(
		decoded,
		automations.StepExecutionCompletion{
			RunID:    automations.RunID("arn_018f47c2-7b3d-7abc-8abc-123456789abc"),
			StepID:   "wait",
			Evidence: automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at.Add(-time.Second)}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = details.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = finished.Validate(); err != nil {
		t.Fatal(err)
	}
	if completedEnv.Phase != "succeeded" || completedEnv.CompletedAt == nil {
		t.Fatalf("completed envelope=%+v", completedEnv)
	}
}

func TestStepExecutionCodecRejectsEnvelopeDisagreement(t *testing.T) {
	t.Parallel()
	if _, err := automations.DecodeStepExecutionDetails(
		"wait",
		automations.StepExecutionEnvelope{
			Kind:         automations.StepKindDelay,
			Phase:        "failed",
			StartedAt:    time.Now().UTC(),
			EvidenceJSON: json.RawMessage(`{}`),
		},
	); err == nil {
		t.Fatal("accepted phase mismatch")
	}
}

func TestPrepareStepExecutionRejectsInvalidReachedEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		evidence automations.InitialStepEvidence
	}{
		{"missing evidence", nil},
		{"missing time", automations.DelayStarted{}},
		{"unreserved interruption code", automations.CommandStopped{At: at, FailureCode: "provider_error"}},
		{"invalid Attempt identity", automations.NotificationStarted{StartedAt: at, AttemptID: "bad"}},
		{"invalid Invocation identity", automations.FunctionStarted{StartedAt: at, InvocationID: "bad"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			details, envelope, err := automations.PrepareStepExecutionDetails(automations.StepExecutionRecord{
				RunID: "arn_018f47c2-7b3d-7abc-8abc-123456789abc", StepID: "step", Evidence: test.evidence,
			})
			if !errors.Is(err, automations.ErrInvalidAutomation) || details != nil || envelope.Kind != "" {
				t.Fatalf("invalid preparation returned details=%v envelope=%+v error=%v", details, envelope, err)
			}
		})
	}
}

func TestCompleteStepDetailsRequiresMatchingRunningEvidence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	const runID = automations.RunID("arn_018f47c2-7b3d-7abc-8abc-123456789abc")
	for _, test := range []struct {
		name    string
		details automations.StepExecutionDetails
		finish  automations.CompletedStepEvidence
	}{
		{"completed target", automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{CompletedAt: at}}, automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}}},
		{"wrong completion kind", automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}}, automations.CommandFinished{}},
		{"invalid terminal state", automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}}, automations.DelayFinished{State: automations.DelaySucceeded{}}},
		{"invalid current value", automations.DelayExecutionDetails{State: automations.DelayRunning{}}, automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}}},
		{"branch is insert once", automations.BranchExecutionDetails{Decision: automations.BranchDecision{StepID: "step", EvaluatedAt: at, Body: automations.IfDecision{Result: automations.IfError{FailureCode: "branch_state_read_failed"}}}}, automations.DelayFinished{State: automations.DelaySucceeded{CompletedAt: at}}},
		{"changed Command reservation", automations.CommandExecutionDetails{StartedAt: at, State: automations.CommandRunning{Reservation: automations.CommandReservation{CommandID: "cmd_018f47c2-7b3d-7abc-8abc-123456789abc", CorrelationID: "cor_018f47c2-7b3d-7abc-8abc-123456789abc"}}}, automations.CommandFinished{State: automations.CommandFailed{CompletedAt: at, FailureCode: "provider_error", Reservation: automations.CommandReservation{CommandID: "cmd_018f47c2-7b3d-7abc-8abc-123456789abd", CorrelationID: "cor_018f47c2-7b3d-7abc-8abc-123456789abc"}}}},
		{"changed Attempt identity", automations.NotificationExecutionDetails{StartedAt: at, AttemptID: "ntf_018f47c2-7b3d-7abc-8abc-123456789abc", State: automations.NotificationRunning{}}, automations.NotificationFinished{AttemptID: "ntf_018f47c2-7b3d-7abc-8abc-123456789abd", State: automations.NotificationSucceeded{CompletedAt: at}}},
		{"changed Invocation identity", automations.FunctionExecutionDetails{StartedAt: at, InvocationID: "afi_018f47c2-7b3d-7abc-8abc-123456789abc", State: automations.FunctionRunning{}}, automations.FunctionFinished{InvocationID: "afi_018f47c2-7b3d-7abc-8abc-123456789abd", State: automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: "afc_018f47c2-7b3d-7abc-8abc-123456789abc"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			details, envelope, err := automations.CompleteStepDetails(test.details, automations.StepExecutionCompletion{
				RunID: runID, StepID: "step", Evidence: test.finish,
			})
			if !errors.Is(err, automations.ErrInvalidAutomation) || details != nil || envelope.Kind != "" {
				t.Fatalf("invalid completion returned details=%v envelope=%+v error=%v", details, envelope, err)
			}
		})
	}
}
