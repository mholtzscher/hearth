package automations_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type executionValidationCase struct {
	name    string
	details automations.StepExecutionDetails
	valid   bool
}

func executionValidationCases() []executionValidationCase {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	const commandID devices.CommandID = "cmd_018f47c2-7b3d-7abc-8abc-123456789abc"
	const attemptID notifications.AttemptID = "ntf_018f47c2-7b3d-7abc-8abc-123456789abc"
	const invocationID adapters.InvocationID = "afi_018f47c2-7b3d-7abc-8abc-123456789abc"
	const catalogID adapters.CatalogID = "afc_018f47c2-7b3d-7abc-8abc-123456789abc"
	reservation := automations.CommandReservation{
		CommandID: commandID, CorrelationID: "cor_018f47c2-7b3d-7abc-8abc-123456789abc",
	}
	branch := func(evaluated, observed time.Time, selected json.RawMessage) automations.BranchExecutionDetails {
		return automations.BranchExecutionDetails{Decision: automations.BranchDecision{
			StepID: "step", EvaluatedAt: evaluated,
			Body: automations.IfDecision{Result: automations.IfSelected{
				Arm: automations.IfThen,
				Evaluation: automations.ConditionEvaluation{
					EvaluatedAt: evaluated, Result: automations.ConditionTrue,
					Nodes: []automations.ConditionNodeResult{{ID: "state", Evidence: automations.KnownStateEvidence{
						Matched: true, SelectedValue: selected,
						Observation: automations.ObservationEvidence{
							ObservationID: "obs_018f47c2-7b3d-7abc-8abc-123456789abc", ObservedAt: observed,
						},
					}}},
				},
			}},
		}}
	}
	return []executionValidationCase{
		{
			"command running",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State:     automations.CommandRunning{Reservation: reservation},
			},
			true,
		},
		{
			"command success",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandSucceeded{
					CompletedAt: at.Add(-time.Second),
					Reservation: reservation,
					Outcome:     automations.SatisfiedCommand{VerifiedCommandID: commandID},
				},
			},
			true,
		},
		{
			"command missing reservation",
			automations.CommandExecutionDetails{StartedAt: at, State: automations.CommandRunning{}},
			false,
		},
		{
			"command mismatched verified identity",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandSucceeded{
					CompletedAt: at,
					Reservation: reservation,
					Outcome: automations.DispatchedCommand{
						VerifiedCommandID: "cmd_018f47c2-7b3d-7abc-8abc-123456789abd",
					},
				},
			},
			false,
		},
		{"command nil state", automations.CommandExecutionDetails{StartedAt: at}, false},
		{
			"command invalid start",
			automations.CommandExecutionDetails{State: automations.CommandRunning{Reservation: reservation}},
			false,
		},
		{
			"command unreserved interruption",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State:     automations.CommandInterrupted{CompletedAt: at, FailureCode: "core_stopping"},
			},
			true,
		},
		{
			"command unreserved failure code",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State:     automations.CommandInterrupted{CompletedAt: at, FailureCode: "provider_error"},
			},
			false,
		},
		{"delay running", automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}}, true},
		{
			"delay succeeded backward clock",
			automations.DelayExecutionDetails{
				StartedAt: at,
				State:     automations.DelaySucceeded{CompletedAt: at.Add(-time.Second)},
			},
			true,
		},
		{"delay nil state", automations.DelayExecutionDetails{StartedAt: at}, false},
		{
			"delay invalid completion",
			automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{}},
			false,
		},
		{
			"delay invalid interruption",
			automations.DelayExecutionDetails{
				StartedAt: at,
				State:     automations.DelayInterrupted{CompletedAt: at, FailureCode: "provider_error"},
			},
			false,
		},
		{
			"notification running",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attemptID,
				State:     automations.NotificationRunning{},
			},
			true,
		},
		{
			"notification accepted",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attemptID,
				State:     automations.NotificationSucceeded{CompletedAt: at},
			},
			true,
		},
		{
			"notification bad identity",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: "bad",
				State:     automations.NotificationRunning{},
			},
			false,
		},
		{
			"notification nil state",
			automations.NotificationExecutionDetails{StartedAt: at, AttemptID: attemptID},
			false,
		},
		{
			"notification invalid failure",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attemptID,
				State: automations.NotificationFailed{
					CompletedAt: at,
					Evidence:    automations.NotificationAttemptFailed{Outcome: notifications.Accepted},
				},
			},
			false,
		},
		{
			"function running",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocationID,
				State:        automations.FunctionRunning{},
			},
			true,
		},
		{
			"function succeeded",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocationID,
				State:        automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: catalogID},
			},
			true,
		},
		{
			"function bad identity",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: "bad",
				State:        automations.FunctionRunning{},
			},
			false,
		},
		{"function nil state", automations.FunctionExecutionDetails{StartedAt: at, InvocationID: invocationID}, false},
		{
			"function invalid catalog",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocationID,
				State:        automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: "bad"},
			},
			false,
		},
		{"branch selected null", branch(at, at, json.RawMessage("null")), true},
		{"branch exact number", branch(at, at, json.RawMessage("9007199254740993")), true},
		{"branch invalid selected value", branch(at, at, json.RawMessage("{")), false},
		{"branch invalid JSON evaluation year", branch(at.AddDate(10000, 0, 0), at, json.RawMessage("true")), false},
		{"branch invalid JSON Observation year", branch(at, at.AddDate(10000, 0, 0), json.RawMessage("true")), false},
		{"nil details", nil, false},
		{
			"pointer details",
			&automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}},
			false,
		},
		{"nil pointer details", (*automations.DelayExecutionDetails)(nil), false},
	}
}

// This corpus also runs unchanged against the pre-refactor checkout. Its logged
// envelopes and error classes provide a deterministic old/new comparison.
func TestExecutionValidationBoundaryCorpus(t *testing.T) {
	t.Parallel()
	for _, test := range executionValidationCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope, err := automations.EncodeStepExecutionDetails(test.details)
			if (err == nil) != test.valid {
				t.Fatalf("encode error = %v, want valid=%v", err, test.valid)
			}
			if err != nil {
				t.Logf(
					"EQUIVALENCE %s invalid_automation=%t",
					test.name,
					errors.Is(err, automations.ErrInvalidAutomation),
				)
				return
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
				t.Fatalf("round trip changed envelope: %+v -> %+v", envelope, reencoded)
			}
			wire := fmt.Sprintf("%s|%s|%s|%v|%s", envelope.Kind, envelope.Phase,
				envelope.StartedAt.Format(time.RFC3339Nano), envelope.CompletedAt, envelope.EvidenceJSON)
			t.Logf("EQUIVALENCE %s %x", test.name, sha256.Sum256([]byte(wire)))
			assertExecutionDecodeRejections(t, envelope)
		})
	}
}

func assertExecutionDecodeRejections(t *testing.T, envelope automations.StepExecutionEnvelope) {
	t.Helper()
	mutations := map[string]func(*automations.StepExecutionEnvelope){
		"unknown kind":  func(value *automations.StepExecutionEnvelope) { value.Kind = "unknown" },
		"unknown phase": func(value *automations.StepExecutionEnvelope) { value.Phase = "unknown" },
		"missing start": func(value *automations.StepExecutionEnvelope) { value.StartedAt = time.Time{} },
		"trailing JSON": func(value *automations.StepExecutionEnvelope) { value.EvidenceJSON = json.RawMessage(`{} {}`) },
		"completion contradiction": func(value *automations.StepExecutionEnvelope) {
			if value.CompletedAt == nil {
				at := value.StartedAt
				value.CompletedAt = &at
			} else {
				value.CompletedAt = nil
			}
		},
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.EvidenceJSON, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"command_id", "correlation_id", "verified_command_id", "attempt_id", "invocation_id", "catalog_revision"} {
		if _, present := fields[field]; !present {
			continue
		}
		body := maps.Clone(fields)
		body[field] = json.RawMessage(`"bad"`)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		mutations["invalid "+field] = func(value *automations.StepExecutionEnvelope) {
			value.EvidenceJSON = raw
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			malformed := envelope
			mutate(&malformed)
			_, err := automations.DecodeStepExecutionDetails("step", malformed)
			if err == nil {
				t.Fatalf("accepted malformed envelope %+v", malformed)
			}
			t.Logf("EQUIVALENCE decode %s %s %s invalid_automation=%t", envelope.Kind, envelope.Phase,
				name, errors.Is(err, automations.ErrInvalidAutomation))
		})
	}
}

func TestExecutionTraceValidationCorpus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*automations.BranchDecision)
		valid  bool
	}{
		{name: "valid branch prefix", valid: true},
		{name: "unencodable branch evaluation", mutate: func(decision *automations.BranchDecision) {
			decision.EvaluatedAt = decision.EvaluatedAt.AddDate(10000, 0, 0)
			mutateIfEvaluation(decision, func(evaluation *automations.ConditionEvaluation) {
				evaluation.EvaluatedAt = decision.EvaluatedAt
			})
		}},
		{name: "unencodable branch Observation", mutate: func(decision *automations.BranchDecision) {
			mutateKnownState(decision, func(evidence *automations.KnownStateEvidence) {
				evidence.Observation.ObservedAt = evidence.Observation.ObservedAt.AddDate(10000, 0, 0)
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot, decision := stateDecisionFixture(t)
			prepared, err := automations.NormalizeDefinition(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if test.mutate != nil {
				test.mutate(&decision)
			}
			err = automations.ValidateStepExecutionTraceWithPreparedSnapshot(prepared, nil,
				automations.RunRunning, []automations.StepExecution{{StepID: decision.StepID,
					Details: automations.BranchExecutionDetails{Decision: decision}}})
			if (err == nil) != test.valid {
				t.Fatalf("trace error = %v, want valid=%v", err, test.valid)
			}
			t.Logf("EQUIVALENCE trace %s accepted=%t invalid_automation=%t", test.name, err == nil,
				errors.Is(err, automations.ErrInvalidAutomation))
		})
	}
}
