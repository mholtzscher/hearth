package automations_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	automations "github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestStepExecutionCodecCommandVariants(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	id := devices.CommandID("cmd_018f47c2-7b3d-7abc-8abc-123456789abc")
	corr := devices.CorrelationID("cor_018f47c2-7b3d-7abc-8abc-123456789abc")
	reserve := automations.CommandReservation{CommandID: id, CorrelationID: corr}
	verified := id
	cases := []struct {
		name    string
		details automations.StepExecutionDetails
		phase   string
	}{
		{
			"running",
			automations.CommandExecutionDetails{StartedAt: at, State: automations.CommandRunning{Reservation: reserve}},
			"running",
		},
		{
			"satisfied",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandSucceeded{
					CompletedAt: at.Add(-time.Second),
					Reservation: reserve,
					Outcome:     automations.SatisfiedCommand{VerifiedCommandID: id},
				},
			},
			"succeeded",
		},
		{
			"dispatched",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandSucceeded{
					CompletedAt: at,
					Reservation: reserve,
					Outcome:     automations.DispatchedCommand{VerifiedCommandID: id},
				},
			},
			"succeeded",
		},
		{
			"failed-unverified",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandFailed{
					CompletedAt: at,
					Reservation: reserve,
					FailureCode: "internal_error",
				},
			},
			"failed",
		},
		{
			"failed-verified",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandFailed{
					CompletedAt:       at,
					Reservation:       reserve,
					FailureCode:       "internal_error",
					VerifiedCommandID: &verified,
				},
			},
			"failed",
		},
		{
			"interrupted-before-reservation",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandInterrupted{
					CompletedAt: at,
					FailureCode: automations.FailureCoreStopping,
				},
			},
			"interrupted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, err := automations.EncodeStepExecutionDetails(tc.details)
			if err != nil {
				t.Fatal(err)
			}
			if env.Phase != tc.phase {
				t.Fatalf("phase=%s", env.Phase)
			}
			got, err := automations.DecodeStepExecutionDetails("turn-on", env)
			if err != nil {
				t.Fatal(err)
			}
			again, err := automations.EncodeStepExecutionDetails(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(again.EvidenceJSON) != string(env.EvidenceJSON) {
				t.Fatalf("payload changed: %s -> %s", env.EvidenceJSON, again.EvidenceJSON)
			}
		})
	}
}

func TestStepExecutionCodecStrictEnvelopeAndCommandReservation(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	base := automations.StepExecutionEnvelope{
		Kind:      automations.StepKindCommand,
		Phase:     "running",
		StartedAt: at,
		EvidenceJSON: json.RawMessage(
			`{"command_id":"cmd_018f47c2-7b3d-7abc-8abc-123456789abc","correlation_id":"cor_018f47c2-7b3d-7abc-8abc-123456789abc"}`,
		),
	}
	for name, raw := range map[string]string{"unknown": "{\"command_id\":\"cmd_018f47c2-7b3d-7abc-8abc-123456789abc\",\"correlation_id\":\"cor_018f47c2-7b3d-7abc-8abc-123456789abc\",\"extra\":1}", "trailing": "{\"command_id\":\"cmd_018f47c2-7b3d-7abc-8abc-123456789abc\",\"correlation_id\":\"cor_018f47c2-7b3d-7abc-8abc-123456789abc\"} {}", "partial reservation": "{\"command_id\":\"cmd_018f47c2-7b3d-7abc-8abc-123456789abc\"}"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := base
			env.EvidenceJSON = json.RawMessage(raw)
			if _, err := automations.DecodeStepExecutionDetails("turn-on", env); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
}

func TestStepExecutionCodecOtherKindRoundTrips(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	later := at.Add(-time.Second)
	attempt := notifications.AttemptID("ntf_018f47c2-7b3d-7abc-8abc-123456789abc")
	invocation := adapters.InvocationID("afi_018f47c2-7b3d-7abc-8abc-123456789abc")
	catalog := adapters.CatalogID("afc_018f47c2-7b3d-7abc-8abc-123456789abc")
	rejected, unknown := notifications.Rejected, notifications.Unknown
	commandCases := []struct {
		name    string
		details automations.StepExecutionDetails
		kind    automations.StepKind
		phase   string
	}{
		{
			"delay-running",
			automations.DelayExecutionDetails{StartedAt: at, State: automations.DelayRunning{}},
			automations.StepKindDelay,
			"running",
		},
		{
			"delay-completed-clock-regression",
			automations.DelayExecutionDetails{StartedAt: at, State: automations.DelaySucceeded{CompletedAt: later}},
			automations.StepKindDelay,
			"succeeded",
		},
		{
			"delay-interrupted",
			automations.DelayExecutionDetails{
				StartedAt: at,
				State: automations.DelayInterrupted{
					CompletedAt: later,
					FailureCode: automations.FailureCoreRestarted,
				},
			},
			automations.StepKindDelay,
			"interrupted",
		},
		{
			"notification-running",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State:     automations.NotificationRunning{},
			},
			automations.StepKindNotification,
			"running",
		},
		{
			"notification-accepted",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State:     automations.NotificationSucceeded{CompletedAt: later},
			},
			automations.StepKindNotification,
			"succeeded",
		},
		{
			"notification-rejected",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State: automations.NotificationFailed{
					CompletedAt: later,
					Evidence: automations.NotificationAttemptFailed{
						Outcome:    rejected,
						ReasonCode: "provider_rejected",
					},
				},
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-unknown",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State: automations.NotificationFailed{
					CompletedAt: later,
					Evidence:    automations.NotificationAttemptFailed{Outcome: unknown, ReasonCode: "outcome_timeout"},
				},
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-preadmission-failure",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State: automations.NotificationFailed{
					CompletedAt: later,
					Evidence: automations.NotificationAdmissionRejected{
						ReasonCode: automations.FailureDestinationNotFound,
					},
				},
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-interrupted",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: attempt,
				State: automations.NotificationInterrupted{
					CompletedAt: later,
					ReasonCode:  automations.FailureCoreRestarted,
				},
			},
			automations.StepKindNotification,
			"interrupted",
		},
		{
			"function-running",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State:        automations.FunctionRunning{},
			},
			automations.StepKindFunction,
			"running",
		},
		{
			"function-succeeded",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State:        automations.FunctionSucceeded{CompletedAt: later, CatalogRevision: catalog},
			},
			automations.StepKindFunction,
			"succeeded",
		},
		{
			"function-admission-failed",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State: automations.FunctionFailed{
					CompletedAt: later,
					Evidence:    automations.FunctionAdmissionRejected{ReasonCode: "adapter_not_found"},
				},
			},
			automations.StepKindFunction,
			"failed",
		},
		{
			"function-provider-failed",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State: automations.FunctionFailed{
					CompletedAt: later,
					Evidence: automations.FunctionInvocationFailed{
						CatalogRevision: catalog,
						Outcome:         adapters.Failed{ReasonCode: "failure"},
					},
				},
			},
			automations.StepKindFunction,
			"failed",
		},
		{
			"function-not-executed",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State: automations.FunctionFailed{
					CompletedAt: later,
					Evidence: automations.FunctionInvocationFailed{
						CatalogRevision: catalog,
						Outcome:         adapters.NotExecuted{ReasonCode: "not_executed"},
					},
				},
			},
			automations.StepKindFunction,
			"failed",
		},
		{
			"function-unknown",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State: automations.FunctionFailed{
					CompletedAt: later,
					Evidence: automations.FunctionInvocationFailed{
						CatalogRevision: catalog,
						Outcome:         adapters.Unknown{ReasonCode: "unknown"},
					},
				},
			},
			automations.StepKindFunction,
			"failed",
		},
		{
			"function-interrupted",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: invocation,
				State: automations.FunctionInterrupted{
					CompletedAt: later,
					ReasonCode:  automations.FailureCoreRestarted,
				},
			},
			automations.StepKindFunction,
			"interrupted",
		},
	}
	for _, tc := range commandCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, err := automations.EncodeStepExecutionDetails(tc.details)
			if err != nil {
				t.Fatal(err)
			}
			if env.Kind != tc.kind || env.Phase != tc.phase {
				t.Fatalf("envelope kind/phase=%s/%s", env.Kind, env.Phase)
			}
			got, err := automations.DecodeStepExecutionDetails("effect", env)
			if err != nil {
				t.Fatal(err)
			}
			round, err := automations.EncodeStepExecutionDetails(got)
			if err != nil {
				t.Fatal(err)
			}
			if round.Kind != env.Kind || round.Phase != env.Phase ||
				string(round.EvidenceJSON) != string(env.EvidenceJSON) {
				t.Fatalf("roundtrip differs: %#v %#v", env, round)
			}
		})
	}
}

func TestStepExecutionCodecBranchOutcomes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	falseEval := automations.ConditionEvaluation{
		EvaluatedAt: at,
		Result:      automations.ConditionFalse,
		Nodes: []automations.ConditionNodeResult{
			{ID: "match", Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{}}},
		},
	}
	trueEval := automations.ConditionEvaluation{
		EvaluatedAt: at,
		Result:      automations.ConditionTrue,
		Nodes: []automations.ConditionNodeResult{
			{
				ID:       "match",
				Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"trig"}},
			},
		},
	}
	unknownEval := automations.ConditionEvaluation{
		EvaluatedAt: at,
		Result:      automations.ConditionUnknown,
		Nodes: []automations.ConditionNodeResult{
			{ID: "match", Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{}}},
		},
	}
	cases := []struct {
		name     string
		kind     automations.StepKind
		decision automations.BranchDecision
		phase    string
	}{
		{
			"if-selection",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: trueEval},
				},
			},
			"succeeded",
		},
		{
			"if-no-match",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfNoMatch, Evaluation: falseEval},
				},
			},
			"succeeded",
		},
		{
			"if-unknown",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body:        automations.IfDecision{Result: automations.IfUnknown{Evaluation: unknownEval}},
			},
			"failed",
		},
		{
			"if-error",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.IfDecision{
					Result: automations.IfError{FailureCode: "branch_state_read_failed"},
				},
			},
			"failed",
		},
		{
			"choose-selection",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseSelected{
						BranchID:    "alt",
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: trueEval}},
					},
				},
			},
			"succeeded",
		},
		{
			"choose-no-match",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseFallback{
						Arm:         automations.ChooseNoMatch,
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: falseEval}},
					},
				},
			},
			"succeeded",
		},
		{
			"choose-default",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseFallback{
						Arm:         automations.ChooseDefault,
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: falseEval}},
					},
				},
			},
			"succeeded",
		},
		{
			"choose-unknown",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseUnknown{
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: unknownEval}},
					},
				},
			},
			"failed",
		},
		{
			"choose-error",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseError{
						FailureCode: "branch_state_read_failed",
						Evaluations: []automations.ChooseEvaluation{},
					},
				},
			},
			"failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertBranchExecutionRoundTrip(t, tc.decision, tc.kind, tc.phase)
		})
	}
}

func assertBranchExecutionRoundTrip(
	t *testing.T,
	decision automations.BranchDecision,
	kind automations.StepKind,
	phase string,
) {
	t.Helper()
	env, err := automations.EncodeStepExecutionDetails(automations.BranchExecutionDetails{Decision: decision})
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != kind || env.Phase != phase || env.CompletedAt == nil ||
		!env.StartedAt.Equal(*env.CompletedAt) {
		t.Fatalf("bad envelope: %#v", env)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(env.EvidenceJSON, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"step_id", "position", "kind", "evaluated_at"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("payload duplicated %s", key)
		}
	}
	var outcome string
	if err = json.Unmarshal(payload["outcome"], &outcome); err != nil || outcome != string(decision.Outcome()) {
		t.Fatalf("outcome=%q err=%v want=%q", outcome, err, decision.Outcome())
	}
	got, err := automations.DecodeStepExecutionDetails(decision.StepID, env)
	if err != nil {
		t.Fatal(err)
	}
	decoded := got.(automations.BranchExecutionDetails).Decision
	if decoded.StepID != decision.StepID || decoded.Kind() != kind || decoded.Outcome() != decision.Outcome() {
		t.Fatalf("decision changed: %#v", decoded)
	}
}

func TestStepExecutionCodecRejectsInvalidShapes(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	bad := time.Time{}
	cases := []struct {
		name    string
		details automations.StepExecutionDetails
	}{
		{"nil-details", nil},
		{"delay-zero-time", automations.DelayExecutionDetails{StartedAt: bad, State: automations.DelayRunning{}}},
		{
			"notification-bad-id",
			automations.NotificationExecutionDetails{
				StartedAt: at,
				AttemptID: "bad",
				State:     automations.NotificationRunning{},
			},
		},
		{
			"function-bad-id",
			automations.FunctionExecutionDetails{
				StartedAt:    at,
				InvocationID: "bad",
				State:        automations.FunctionRunning{},
			},
		},
		{
			"command-invalid-reservation-running",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State:     automations.CommandRunning{Reservation: automations.CommandReservation{CommandID: "bad"}},
			},
		},
		{
			"command-success-no-verified-link",
			automations.CommandExecutionDetails{
				StartedAt: at,
				State: automations.CommandSucceeded{CompletedAt: at, Reservation: automations.CommandReservation{
					CommandID:     devices.CommandID("cmd_018f47c2-7b3d-7abc-8abc-123456789abc"),
					CorrelationID: devices.CorrelationID("cor_018f47c2-7b3d-7abc-8abc-123456789abc"),
				}, Outcome: automations.SatisfiedCommand{VerifiedCommandID: ""}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := automations.EncodeStepExecutionDetails(tc.details); err == nil {
				t.Fatal("accepted invalid details")
			}
		})
	}
	good := automations.StepExecutionEnvelope{
		Kind:         automations.StepKindDelay,
		Phase:        "running",
		StartedAt:    at,
		EvidenceJSON: json.RawMessage(`{}`),
	}
	for name, mutate := range map[string]func(*automations.StepExecutionEnvelope){"bad-kind": func(e *automations.StepExecutionEnvelope) { e.Kind = "bogus" }, "bad-phase": func(e *automations.StepExecutionEnvelope) { e.Phase = "failed" }, "missing-time": func(e *automations.StepExecutionEnvelope) { e.StartedAt = bad }, "completion-on-running": func(e *automations.StepExecutionEnvelope) { x := at; e.CompletedAt = &x }, "unknown-field": func(e *automations.StepExecutionEnvelope) {
		e.EvidenceJSON = json.RawMessage(`{"reason_code":"core_restarted","extra":1}`)
	}, "trailing-json": func(e *automations.StepExecutionEnvelope) { e.EvidenceJSON = json.RawMessage(`{} {}`) }} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := good
			mutate(&e)
			if _, err := automations.DecodeStepExecutionDetails("delay", e); err == nil {
				t.Fatal("accepted malformed envelope")
			}
		})
	}
}
