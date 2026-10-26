package automations_test

import (
	"encoding/json"
	"testing"
	"time"

	automations "github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestStepExecutionDelayCodecAndCompletion(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	details, err := automations.RecordStepDetails(
		automations.StepExecutionRecord{
			RunID:    automations.RunID("arn_018f47c2-7b3d-7abc-8abc-123456789abc"),
			StepID:   "wait",
			Evidence: automations.DelayStarted{StartedAt: at},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := automations.EncodeStepExecutionDetails(details)
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
	finished, err := automations.CompleteStepDetails(
		decoded,
		automations.StepExecutionCompletion{
			RunID:    automations.RunID("arn_018f47c2-7b3d-7abc-8abc-123456789abc"),
			StepID:   "wait",
			Evidence: automations.DelayFinished{CompletedAt: at.Add(-time.Second)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	completedEnv, err := automations.EncodeStepExecutionDetails(finished)
	if err != nil {
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
