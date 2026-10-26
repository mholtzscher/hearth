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
	reserve := &automations.CommandReservation{CommandID: id, CorrelationID: corr}
	verified := id
	cases := []struct {
		name  string
		body  automations.StepExecutionBody
		phase string
	}{
		{"running", automations.CommandExecution{StartedAt: at, Reservation: reserve}, "running"},
		{
			"satisfied",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: new(at.Add(-time.Second)),
				Reservation: reserve,
				Outcome:     automations.SatisfiedStep{VerifiedCommandID: id},
			},
			"succeeded",
		},
		{
			"dispatched",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: &at,
				Reservation: reserve,
				Outcome:     automations.DispatchedStep{VerifiedCommandID: id},
			},
			"succeeded",
		},
		{
			"failed-unverified",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: &at,
				Reservation: reserve,
				Outcome:     automations.FailedStep{FailureCode: "internal_error"},
			},
			"failed",
		},
		{
			"failed-verified",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: &at,
				Reservation: reserve,
				Outcome:     automations.FailedStep{FailureCode: "internal_error", VerifiedCommandID: &verified},
			},
			"failed",
		},
		{
			"interrupted-before-reservation",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: &at,
				Outcome:     automations.InterruptedStep{FailureCode: automations.FailureCoreStopping},
			},
			"interrupted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, err := automations.EncodeStepExecutionBody(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if env.Phase != tc.phase {
				t.Fatalf("phase=%s", env.Phase)
			}
			got, err := automations.DecodeStepExecutionBody("turn-on", env)
			if err != nil {
				t.Fatal(err)
			}
			again, err := automations.EncodeStepExecutionBody(got)
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
			if _, err := automations.DecodeStepExecutionBody("turn-on", env); err == nil {
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
	accepted, rejected, unknown := notifications.Accepted, notifications.Rejected, notifications.Unknown
	commandCases := []struct {
		name  string
		body  automations.StepExecutionBody
		kind  automations.StepKind
		phase string
	}{
		{"delay-running", automations.DelayExecutionBody{StartedAt: at}, automations.StepKindDelay, "running"},
		{
			"delay-completed-clock-regression",
			automations.DelayExecutionBody{StartedAt: at, CompletedAt: &later},
			automations.StepKindDelay,
			"succeeded",
		},
		{
			"delay-interrupted",
			automations.DelayExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				FailureCode: new(automations.FailureCoreRestarted),
			},
			automations.StepKindDelay,
			"interrupted",
		},
		{
			"notification-running",
			automations.NotificationExecutionBody{StartedAt: at, AttemptID: attempt},
			automations.StepKindNotification,
			"running",
		},
		{
			"notification-accepted",
			automations.NotificationExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				AttemptID:   attempt,
				Outcome:     &accepted,
			},
			automations.StepKindNotification,
			"succeeded",
		},
		{
			"notification-rejected",
			automations.NotificationExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				AttemptID:   attempt,
				Outcome:     &rejected,
				ReasonCode:  new("provider_rejected"),
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-unknown",
			automations.NotificationExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				AttemptID:   attempt,
				Outcome:     &unknown,
				ReasonCode:  new("outcome_timeout"),
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-preadmission-failure",
			automations.NotificationExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				AttemptID:   attempt,
				ReasonCode:  new(automations.FailureDestinationNotFound),
			},
			automations.StepKindNotification,
			"failed",
		},
		{
			"notification-interrupted",
			automations.NotificationExecutionBody{
				StartedAt:   at,
				CompletedAt: &later,
				AttemptID:   attempt,
				ReasonCode:  new(automations.FailureCoreRestarted),
			},
			automations.StepKindNotification,
			"interrupted",
		},
		{
			"function-running",
			automations.FunctionExecutionBody{
				StartedAt:    at,
				InvocationID: invocation,
				State:        automations.FunctionRunning{},
			},
			automations.StepKindFunction,
			"running",
		},
		{
			"function-succeeded",
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
				InvocationID: invocation,
				State:        automations.FunctionSucceeded{CompletedAt: later, CatalogRevision: catalog},
			},
			automations.StepKindFunction,
			"succeeded",
		},
		{
			"function-admission-failed",
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
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
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
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
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
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
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
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
			automations.FunctionExecutionBody{
				StartedAt:    at,
				CompletedAt:  &later,
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
			env, err := automations.EncodeStepExecutionBody(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if env.Kind != tc.kind || env.Phase != tc.phase {
				t.Fatalf("envelope kind/phase=%s/%s", env.Kind, env.Phase)
			}
			got, err := automations.DecodeStepExecutionBody("effect", env)
			if err != nil {
				t.Fatal(err)
			}
			round, err := automations.EncodeStepExecutionBody(got)
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
				Position:    0,
				EvaluatedAt: at,
				Body: automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: trueEval},
				},
			},
			"decided",
		},
		{
			"if-no-match",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				Position:    0,
				EvaluatedAt: at,
				Body: automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfNoMatch, Evaluation: falseEval},
				},
			},
			"decided",
		},
		{
			"if-unknown",
			automations.StepKindIf,
			automations.BranchDecision{
				StepID:      "route",
				Position:    0,
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
				Position:    0,
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
				Position:    0,
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseSelected{
						BranchID:    "alt",
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: trueEval}},
					},
				},
			},
			"decided",
		},
		{
			"choose-no-match",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				Position:    0,
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseFallback{
						Arm:         automations.ChooseNoMatch,
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: falseEval}},
					},
				},
			},
			"decided",
		},
		{
			"choose-default",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				Position:    0,
				EvaluatedAt: at,
				Body: automations.ChooseDecision{
					Result: automations.ChooseFallback{
						Arm:         automations.ChooseDefault,
						Evaluations: []automations.ChooseEvaluation{{BranchID: "alt", Evaluation: falseEval}},
					},
				},
			},
			"decided",
		},
		{
			"choose-unknown",
			automations.StepKindChoose,
			automations.BranchDecision{
				StepID:      "route",
				Position:    0,
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
				Position:    0,
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
	env, err := automations.EncodeStepExecutionBody(automations.BranchExecutionBody{Decision: decision})
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
	got, err := automations.DecodeStepExecutionBody(decision.StepID, env)
	if err != nil {
		t.Fatal(err)
	}
	decoded := got.(automations.BranchExecutionBody).Decision
	if decoded.StepID != decision.StepID || decoded.Kind() != kind || decoded.Outcome() != decision.Outcome() ||
		decoded.Position != 0 {
		t.Fatalf("decision changed: %#v", decoded)
	}
}

func TestStepExecutionCodecRejectsInvalidShapes(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	bad := time.Time{}
	cases := []struct {
		name string
		body automations.StepExecutionBody
	}{
		{"nil-body", nil},
		{"delay-zero-time", automations.DelayExecutionBody{StartedAt: bad}},
		{"notification-bad-id", automations.NotificationExecutionBody{StartedAt: at, AttemptID: "bad"}},
		{
			"function-bad-id",
			automations.FunctionExecutionBody{StartedAt: at, InvocationID: "bad", State: automations.FunctionRunning{}},
		},
		{"command-no-reservation-running", automations.CommandExecution{StartedAt: at}},
		{
			"command-success-no-verified-link",
			automations.CommandExecution{
				StartedAt:   at,
				CompletedAt: &at,
				Reservation: &automations.CommandReservation{
					CommandID:     devices.CommandID("cmd_018f47c2-7b3d-7abc-8abc-123456789abc"),
					CorrelationID: devices.CorrelationID("cor_018f47c2-7b3d-7abc-8abc-123456789abc"),
				},
				Outcome: automations.SatisfiedStep{},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := automations.EncodeStepExecutionBody(tc.body); err == nil {
				t.Fatal("accepted invalid body")
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
			if _, err := automations.DecodeStepExecutionBody("delay", e); err == nil {
				t.Fatal("accepted malformed envelope")
			}
		})
	}
}
