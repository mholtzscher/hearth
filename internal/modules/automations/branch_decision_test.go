package automations_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// validateBranchDecision prepares freely constructed test snapshots before using
// the same evidence validator as repository writes and retained history reads.
func validateBranchDecision(
	decision automations.BranchDecision,
	snapshot automations.Definition,
	matchedTriggerIDs []automations.TriggerID,
) error {
	normalized, err := automations.NormalizeDefinition(snapshot)
	if err != nil {
		return err
	}
	return automations.ValidateBranchDecisionWithPreparedSnapshot(decision, normalized, matchedTriggerIDs)
}

func branchDecisionFixture(t *testing.T) (automations.Definition, automations.BranchDecision) {
	t.Helper()
	d := branchingFixture(t)
	d.Triggers = append(d.Triggers, automations.Trigger{ID: "b", Kind: automations.TriggerKindCron,
		Cron: &automations.CronTrigger{Expression: "* * * * *"}})
	c := d.Steps[0]
	d.Steps = []automations.Step{{ID: "route", Kind: automations.StepKindChoose, Choose: &automations.ChooseStep{
		Branches: []automations.ChooseBranch{
			{ID: "first", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "first", 1)},
			{ID: "second", Conditions: triggerPredicate("b", "a"), Steps: commandSequence(c, "second", 1)},
		},
	}}}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	decision := automations.BranchDecision{
		StepID: "route", Kind: automations.StepKindChoose, EvaluatedAt: at, Outcome: automations.BranchChosen,
		SelectedBranchID: new(automations.BranchID("second")),
		Evaluations: []automations.BranchConditionEvaluation{
			triggerEvaluation("first", at, automations.ConditionFalse),
			triggerEvaluation("second", at, automations.ConditionTrue, "b"),
		},
	}
	return d, decision
}

func triggerEvaluation(
	id automations.BranchID,
	at time.Time,
	result automations.ConditionResult,
	ids ...automations.TriggerID,
) automations.BranchConditionEvaluation {
	if ids == nil {
		ids = []automations.TriggerID{}
	}
	return automations.BranchConditionEvaluation{
		BranchID: &id, Evaluation: automations.ConditionEvaluation{
			EvaluatedAt: at, Result: result, Nodes: []automations.ConditionNodeResult{
				{ID: "match", Result: result, Trigger: &automations.TriggerConditionEvidence{MatchedTriggerIDs: ids}},
			},
		},
	}
}

// Retained evidence is independent of live State. The wire example and A4
// specify array intersections and the immutable, definition-order false prefix.
func TestBranchDecisionRetainedRoundTrip(t *testing.T) {
	t.Parallel()
	d, decision := branchDecisionFixture(t)
	if err := validateBranchDecision(decision, d, []automations.TriggerID{"b"}); err != nil {
		t.Fatal(err)
	}
	raw, err := automations.EncodeBranchDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"matched_trigger_ids":[]`) || strings.Contains(string(raw), `"failure_code"`) {
		t.Fatalf("retained wire shape = %s", raw)
	}
	decoded, err := automations.DecodeBranchDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, decision) {
		t.Fatalf("decoded = %#v, want %#v", decoded, decision)
	}
	if err = validateBranchDecision(decoded, d, []automations.TriggerID{"b"}); err != nil {
		t.Fatal(err)
	}
}

func TestBranchDecisionRequiresConfiguredIntersectionOrder(t *testing.T) {
	t.Parallel()
	d, b := branchDecisionFixture(t)
	child := d.Steps[0].Choose.Branches[0].Conditions
	d.Steps[0].Choose.Branches[0].Conditions = automations.Condition{
		ID:    "negate",
		Kind:  automations.ConditionNot,
		Child: &child,
	}
	b.Evaluations[0].Evaluation.Nodes[0] = automations.ConditionNodeResult{
		ID: "match", Result: automations.ConditionTrue,
		Trigger: &automations.TriggerConditionEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}},
	}
	b.Evaluations[1].Evaluation.Nodes[0].Trigger.MatchedTriggerIDs = []automations.TriggerID{"b", "a"}
	matches := []automations.TriggerID{"a", "b"}
	if err := validateBranchDecision(b, d, matches); err != nil {
		t.Fatal(err)
	}
	// The Run's order is not the leaf's configured order [b,a].
	b.Evaluations[1].Evaluation.Nodes[0].Trigger.MatchedTriggerIDs = []automations.TriggerID{"a", "b"}
	if err := validateBranchDecision(b, d, matches); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("Run-order evidence accepted: %v", err)
	}
}

// Each mutation violates one retained evidence invariant from the spec. These
// cases protect the repository's independent domain boundary, not evaluation.
func TestBranchDecisionRejectsIncoherentSnapshotEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*automations.Definition, *automations.BranchDecision, *[]automations.TriggerID)
	}{
		{
			"negative position",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Position = -1
			},
		},
		{"position bound", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Position = 64
		}},
		{"unknown Step", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.StepID = "gone"
		}},
		{"wrong kind", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Kind = automations.StepKindCommand
		}},
		{
			"wrong outcome kind",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Outcome = automations.BranchThen
				b.SelectedBranchID = nil
			},
		},
		{
			"omitted selection",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.SelectedBranchID = nil
			},
		},
		{"wrong selection", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.SelectedBranchID = new(automations.BranchID("first"))
		}},
		{"time mismatch", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Evaluations[0].Evaluation.EvaluatedAt = b.EvaluatedAt.Add(time.Second)
		}},
		{"zero time", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.EvaluatedAt = time.Time{}
		}},
		{
			"skipped alternative",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Evaluations = b.Evaluations[1:]
			},
		},
		{
			"extra alternative",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Evaluations = append(
					b.Evaluations,
					triggerEvaluation("third", b.EvaluatedAt, automations.ConditionFalse),
				)
			},
		},
		{"true prefix", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Evaluations[0] = triggerEvaluation("first", b.EvaluatedAt, automations.ConditionTrue, "a")
		}},
		{
			"wrong root composition",
			func(d *automations.Definition, _ *automations.BranchDecision, _ *[]automations.TriggerID) {
				child := d.Steps[0].Choose.Branches[1].Conditions
				d.Steps[0].Choose.Branches[1].Conditions = automations.Condition{
					ID:    "negated",
					Kind:  automations.ConditionNot,
					Child: &child,
				}
			},
		},
		{"wrong leaf ID", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Evaluations[1].Evaluation.Nodes[0].ID = "other"
		}},
		{
			"wrong intersection",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Evaluations[1].Evaluation.Nodes[0].Trigger.MatchedTriggerIDs = []automations.TriggerID{"a"}
			},
		},
		{"missing leaf", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.Evaluations[1].Evaluation.Nodes = nil
		}},
		{
			"Trigger State fields",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Evaluations[1].Evaluation.Nodes[0].SelectedValue = json.RawMessage(`null`)
			},
		},
		{
			"success failure code",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.FailureCode = new("branch_condition_unknown")
			},
		},
		{
			"invalid matches",
			func(_ *automations.Definition, _ *automations.BranchDecision, ids *[]automations.TriggerID) {
				*ids = []automations.TriggerID{"absent"}
			},
		},
		{
			"duplicate matches",
			func(_ *automations.Definition, _ *automations.BranchDecision, ids *[]automations.TriggerID) {
				*ids = []automations.TriggerID{"b", "b"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, decision := branchDecisionFixture(t)
			matches := []automations.TriggerID{"b"}
			tc.mutate(&d, &decision, &matches)
			if err := validateBranchDecision(
				decision,
				d,
				matches,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestBranchDecisionStrictRetainedCodec(t *testing.T) {
	t.Parallel()
	const evaluation = `{"evaluated_at":"2026-10-03T12:00:00Z","result":"true","nodes":[{"id":"match","result":"true","trigger":{"matched_trigger_ids":["b"]}}]}`
	const evaluations = `[{"branch_id":"second","evaluation":` + evaluation + `}]`
	const valid = `{"position":0,"step_id":"route","kind":"choose","evaluated_at":"2026-10-03T12:00:00Z","outcome":"branch","selected_branch_id":"second","evaluations":` + evaluations + `}`
	if _, err := automations.DecodeBranchDecision(json.RawMessage(valid)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"null record", `null`}, {"array record", `[]`}, {"trailing", valid + `{}`},
		{"unknown", strings.Replace(valid, `"position":0`, `"extra":0,"position":0`, 1)},
		{"missing position", strings.Replace(valid, `"position":0,`, ``, 1)},
		{"null position", strings.Replace(valid, `"position":0`, `"position":null`, 1)},
		{"missing Step ID", strings.Replace(valid, `"step_id":"route",`, ``, 1)},
		{"missing kind", strings.Replace(valid, `"kind":"choose",`, ``, 1)},
		{"missing outcome", strings.Replace(valid, `"outcome":"branch",`, ``, 1)},
		{"missing decision time", strings.Replace(valid, `"evaluated_at":"2026-10-03T12:00:00Z",`, ``, 1)},
		{"null decision time", strings.Replace(valid, `"evaluated_at":"2026-10-03T12:00:00Z"`, `"evaluated_at":null`, 1)},
		{"missing evaluations", strings.Replace(valid, `,"evaluations":`+evaluations, ``, 1)},
		{"null evaluations", strings.Replace(valid, evaluations, `null`, 1)},
		{"null selection", strings.Replace(valid, `"selected_branch_id":"second"`, `"selected_branch_id":null`, 1)},
		{"null branch ID", strings.Replace(valid, `"branch_id":"second"`, `"branch_id":null`, 1)},
		{"null evaluation", strings.Replace(valid, evaluation, `null`, 1)},
		{"missing evaluation", strings.Replace(valid, `,"evaluation":`+evaluation, ``, 1)},
		{"missing evaluation result", strings.Replace(valid, `"result":"true",`, ``, 1)},
		{"missing evaluation time", strings.Replace(valid, `"evaluation":{"evaluated_at":"2026-10-03T12:00:00Z",`, `"evaluation":{`, 1)},
		{"null nodes", strings.Replace(valid, `[{"id":"match","result":"true","trigger":{"matched_trigger_ids":["b"]}}]`, `null`, 1)},
		{"null Trigger", strings.Replace(valid, `"trigger":{"matched_trigger_ids":["b"]}`, `"trigger":null`, 1)},
		{"missing intersection", strings.Replace(valid, `"matched_trigger_ids":["b"]`, ``, 1)},
		{"null intersection", strings.Replace(valid, `"matched_trigger_ids":["b"]`, `"matched_trigger_ids":null`, 1)},
		{"unknown Trigger field", strings.Replace(valid, `"matched_trigger_ids":["b"]`, `"matched_trigger_ids":["b"],"state":true`, 1)},
		{"missing leaf result", strings.Replace(valid, `"id":"match","result":"true"`, `"id":"match"`, 1)},
		{"null reason", strings.Replace(valid, `"id":"match"`, `"id":"match","unknown_reason":null`, 1)},
		{"null observation", strings.Replace(valid, `"id":"match"`, `"id":"match","observation_id":null`, 1)},
		{"null failure", strings.Replace(valid, `"position":0`, `"position":0,"failure_code":null`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := automations.DecodeBranchDecision(
				json.RawMessage(tc.raw),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("decode = %v", err)
			}
		})
	}
}

func TestNewRunSnapshotUsesStableCommandOnlyAttempts(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = []automations.Step{
		ifNode("outer", []automations.Step{
			{ID: "choose", Kind: automations.StepKindChoose, Choose: &automations.ChooseStep{
				Branches: []automations.ChooseBranch{
					{ID: "one", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "one", 1)},
					{ID: "two", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "two", 1)},
				}, Default: commandSequence(c, "default", 1),
			}},
		}),
		commandSequence(c, "after", 1)[0],
	}
	d.Steps[0].If.Else = commandSequence(c, "else", 1)
	for _, tc := range []struct {
		name string
		d    automations.Definition
		ids  []automations.StepID
	}{
		{"nested", d, []automations.StepID{"one0", "two0", "default0", "else0", "after0"}},
		{"legacy flat", branchingFixture(t), []automations.StepID{c.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			normalized, err := automations.NormalizeDefinition(tc.d)
			if err != nil {
				t.Fatal(err)
			}
			run := automations.NewRunSnapshot(
				automations.Record{Definition: normalized},
				"",
				automations.RunSourceManual,
				nil,
				nil,
				automations.NotConfiguredDecision(),
				time.Now(),
			)
			if len(run.Steps) != len(tc.ids) || run.BranchDecisions == nil || len(run.BranchDecisions) != 0 {
				t.Fatalf("Run attempts/decisions = %#v / %#v", run.Steps, run.BranchDecisions)
			}
			for i, id := range tc.ids {
				want := automations.StepAttempt{Position: i, StepID: id, Status: automations.StepNotAttempted}
				if !reflect.DeepEqual(run.Steps[i], want) {
					t.Fatalf("attempt %d = %#v, want %#v", i, run.Steps[i], want)
				}
			}
		})
	}
}

func stateDecisionFixture(t *testing.T) (automations.Definition, automations.BranchDecision) {
	t.Helper()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = []automations.Step{ifNode("state-if", []automations.Step{c})}
	d.Steps[0].If.Conditions = automations.Condition{
		ID:   "state",
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: c.EntityID,
			Operator: automations.ComparisonEqual,
			Operand:  json.RawMessage(`null`),
		},
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return d, automations.BranchDecision{
		StepID: "state-if", Kind: automations.StepKindIf, EvaluatedAt: at, Outcome: automations.BranchThen,
		Evaluations: []automations.BranchConditionEvaluation{{Evaluation: automations.ConditionEvaluation{
			EvaluatedAt: at, Result: automations.ConditionTrue, Nodes: []automations.ConditionNodeResult{
				{
					ID:            "state",
					Result:        automations.ConditionTrue,
					SelectedValue: json.RawMessage(`null`),
					ObservationID: new(
						devices.ObservationID("obs_01950000-0000-7000-8000-000000000001"),
					),
					ObservedAt: &at,
				},
			},
		}}},
	}
}

func TestBranchDecisionPreservesSelectedJSONNull(t *testing.T) {
	t.Parallel()
	d, decision := stateDecisionFixture(t)
	if err := validateBranchDecision(decision, d, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := automations.EncodeBranchDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"selected_value":null`) {
		t.Fatalf("selected null lost: %s", raw)
	}
	decoded, err := automations.DecodeBranchDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision, decoded) {
		t.Fatalf("decoded = %#v", decoded)
	}
	decision.Evaluations[0].Evaluation.Nodes[0].SelectedValue = nil
	if _, err = automations.EncodeBranchDecision(decision); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("missing selection encoded: %v", err)
	}
}

//nolint:gocognit // The outcome matrix keeps positive cases and their single invalid mutation together.
func TestBranchDecisionFallbackAndFailureContracts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		kind        automations.StepKind
		outcome     automations.BranchOutcome
		fallback    bool
		errorPrefix bool
	}{
		{"If Else", automations.StepKindIf, automations.BranchElse, true, false},
		{"If no Else", automations.StepKindIf, automations.BranchNoMatch, false, false},
		{"Choose Default", automations.StepKindChoose, automations.BranchDefault, true, false},
		{"Choose no Default", automations.StepKindChoose, automations.BranchNoMatch, false, false},
		{"If unknown", automations.StepKindIf, automations.BranchUnknown, false, false},
		{"Choose unknown prefix", automations.StepKindChoose, automations.BranchUnknown, false, false},
		{"If read error", automations.StepKindIf, automations.BranchError, false, false},
		{"Choose incomplete snapshot", automations.StepKindChoose, automations.BranchError, false, false},
		{"Choose corrupt after false", automations.StepKindChoose, automations.BranchError, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, b := branchDecisionFixture(t)
			b.Outcome, b.SelectedBranchID = tc.outcome, nil
			b.Evaluations[1] = triggerEvaluation("second", b.EvaluatedAt, automations.ConditionFalse)
			command := d.Steps[0].Choose.Branches[0].Steps[0]
			if tc.kind == automations.StepKindIf {
				d.Steps = []automations.Step{ifNode("route", []automations.Step{command})}
				b.Kind = tc.kind
				b.Evaluations = b.Evaluations[:1]
				b.Evaluations[0].BranchID = nil
				if tc.fallback {
					d.Steps[0].If.Else = commandSequence(command, "fallback", 1)
				}
			} else if tc.fallback {
				d.Steps[0].Choose.Default = commandSequence(command, "fallback", 1)
			}
			if tc.outcome == automations.BranchUnknown {
				state := automations.Condition{
					ID:   "state",
					Kind: automations.ConditionEntityState,
					EntityState: &automations.EntityStateCondition{
						EntityID: command.EntityID,
						Operator: automations.ComparisonEqual,
						Operand:  json.RawMessage(`true`),
					},
				}
				if tc.kind == automations.StepKindIf {
					d.Steps[0].If.Conditions = state
				} else {
					d.Steps[0].Choose.Branches[1].Conditions = state
				}
				last := &b.Evaluations[len(b.Evaluations)-1].Evaluation
				last.Result = automations.ConditionUnknown
				last.Nodes = []automations.ConditionNodeResult{
					{
						ID:            "state",
						Result:        automations.ConditionUnknown,
						UnknownReason: new(automations.ConditionUnknownStateMissing),
					},
				}
				b.FailureCode = new("branch_condition_unknown")
			}
			if tc.outcome == automations.BranchError {
				b.Evaluations = b.Evaluations[:0]
				b.FailureCode = new("branch_state_read_failed")
				if tc.kind == automations.StepKindChoose {
					b.FailureCode = new("branch_snapshot_incomplete")
				}
				if tc.errorPrefix {
					b.Evaluations = []automations.BranchConditionEvaluation{
						triggerEvaluation("first", b.EvaluatedAt, automations.ConditionFalse),
					}
					b.FailureCode = new("branch_state_corrupt")
				}
			}
			if err := validateBranchDecision(b, d, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := automations.EncodeBranchDecision(b)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := automations.DecodeBranchDecision(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err = validateBranchDecision(decoded, d, nil); err != nil {
				t.Fatal(err)
			}
			// A mismatching fallback or failure code must not survive validation.
			switch tc.outcome {
			case automations.BranchNoMatch:
				if tc.kind == automations.StepKindIf {
					d.Steps[0].If.Else = commandSequence(command, "fallback", 1)
				} else {
					d.Steps[0].Choose.Default = commandSequence(command, "fallback", 1)
				}
			case automations.BranchElse:
				d.Steps[0].If.Else = nil
			case automations.BranchDefault:
				d.Steps[0].Choose.Default = nil
			case automations.BranchThen, automations.BranchChosen, automations.BranchUnknown, automations.BranchError:
				b.FailureCode = new("wrong_failure")
			}
			if err = validateBranchDecision(b, d, nil); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("incoherent fallback/failure accepted: %v", err)
			}
		})
	}
}

func TestBranchDecisionStateLeafShapeAndBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*automations.Definition, *automations.BranchDecision)
	}{
		{"known with reason", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].UnknownReason = new(automations.ConditionUnknownTypeMismatch)
		}},
		{"missing Observation", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].ObservationID = nil
		}},
		{"bad Observation", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].ObservationID = new(devices.ObservationID("bad"))
		}},
		{"zero Observation time", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].ObservedAt = new(time.Time{})
		}},
		{"trailing selection", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].SelectedValue = json.RawMessage(`null true`)
		}},
		{"wrong leaf family", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes = []automations.ConditionNodeResult{{ID: "state", Result: automations.ConditionTrue, Trigger: &automations.TriggerConditionEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}}}}
		}},
		{"leaf bound", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes = make([]automations.ConditionNodeResult, 65)
		}},
		{"cyclic Condition", func(d *automations.Definition, _ *automations.BranchDecision) {
			root := &d.Steps[0].If.Conditions
			*root = automations.Condition{ID: "cycle", Kind: automations.ConditionNot, Child: root}
		}},
		{"cyclic Step", func(d *automations.Definition, _ *automations.BranchDecision) { d.Steps[0].If.Then = d.Steps }},
		{"expired known evidence", func(d *automations.Definition, b *automations.BranchDecision) {
			d.Steps[0].If.Conditions.EntityState.MaxAgeSeconds = new(int64(1))
			b.Evaluations[0].Evaluation.Nodes[0].ObservedAt = new(b.EvaluatedAt.Add(-2 * time.Second))
		}},
		{"incompatible known selection", func(_ *automations.Definition, b *automations.BranchDecision) {
			b.Evaluations[0].Evaluation.Nodes[0].SelectedValue = json.RawMessage(`true`)
		}},
		{"missing State carries Observation", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownStateMissing)
		}},
		{"missing pointer carries selection", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownPointerMissing)
		}},
		{"unknown reason not defined", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownReason("bad_reason"))
		}},
		{"unknown with no reason", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownTypeMismatch)
			b.Evaluations[0].Evaluation.Nodes[0].UnknownReason = nil
		}},
		{"age reason without age bound", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownEvidenceExpired)
		}},
		{"type mismatch with compatible value", func(_ *automations.Definition, b *automations.BranchDecision) {
			setStateDecisionUnknown(b, automations.ConditionUnknownTypeMismatch)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, b := stateDecisionFixture(t)
			tc.mutate(&d, &b)
			if err := validateBranchDecision(b, d, nil); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func setStateDecisionUnknown(b *automations.BranchDecision, reason automations.ConditionUnknownReason) {
	b.Outcome = automations.BranchUnknown
	b.FailureCode = new("branch_condition_unknown")
	b.Evaluations[0].Evaluation.Result = automations.ConditionUnknown
	b.Evaluations[0].Evaluation.Nodes[0].Result = automations.ConditionUnknown
	b.Evaluations[0].Evaluation.Nodes[0].UnknownReason = &reason
}

func TestAdmissionDecisionCodecStillRejectsTriggerEvidence(t *testing.T) {
	t.Parallel()
	d, b := stateDecisionFixture(t)
	root := d.Steps[0].If.Conditions
	evaluation := b.Evaluations[0].Evaluation
	evaluation.Nodes = []automations.ConditionNodeResult{
		{
			ID:      "state",
			Result:  automations.ConditionTrue,
			Trigger: &automations.TriggerConditionEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}},
		},
	}
	if _, err := automations.EncodeConditionDecision(
		automations.EvaluatedDecision(root, evaluation),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("admission encoded Trigger evidence: %v", err)
	}
	// Independent retained fixture uses a valid State-only snapshot but Trigger evidence.
	const raw = `{"mode":"evaluated","bypass_requested":false,"snapshot":{"id":"state","kind":"entity_state","entity_id":"ent_01950000-0000-7000-8000-000000000001","value_pointer":"","operator":"eq","operand":true},"evaluation":{"evaluated_at":"2026-10-03T12:00:00Z","result":"true","nodes":[{"id":"state","result":"true","trigger":{"matched_trigger_ids":["a"]}}]}}`
	if _, err := automations.DecodeConditionDecision(
		json.RawMessage(raw),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("admission decoded Trigger evidence: %v", err)
	}
}

// Three-valued roots are composed from all retained leaves. An unknown leaf
// does not imply an unknown root, and reordering evidence changes its meaning.
func TestBranchDecisionComposesAllRetainedLeaves(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind          automations.ConditionKind
		triggerResult automations.ConditionResult
		matches       []automations.TriggerID
		outcome       automations.BranchOutcome
		rootResult    automations.ConditionResult
	}{
		{automations.ConditionAny, automations.ConditionTrue, []automations.TriggerID{"a"}, automations.BranchThen, automations.ConditionTrue},
		{automations.ConditionAll, automations.ConditionFalse, nil, automations.BranchNoMatch, automations.ConditionFalse},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			d, b := stateDecisionFixture(t)
			state := d.Steps[0].If.Conditions
			d.Steps[0].If.Conditions = automations.Condition{
				ID:       "group",
				Kind:     tc.kind,
				Children: []automations.Condition{triggerPredicate("a"), state},
			}
			b.Outcome = tc.outcome
			b.Evaluations[0].Evaluation.Result = tc.rootResult
			b.Evaluations[0].Evaluation.Nodes = []automations.ConditionNodeResult{
				{
					ID:      "match",
					Result:  tc.triggerResult,
					Trigger: &automations.TriggerConditionEvidence{MatchedTriggerIDs: tc.matches},
				},
				{
					ID:            "state",
					Result:        automations.ConditionUnknown,
					UnknownReason: new(automations.ConditionUnknownStateMissing),
				},
			}
			if err := validateBranchDecision(b, d, tc.matches); err != nil {
				t.Fatal(err)
			}
			nodes := b.Evaluations[0].Evaluation.Nodes
			nodes[0], nodes[1] = nodes[1], nodes[0]
			if err := validateBranchDecision(
				b,
				d,
				tc.matches,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("out-of-order evidence accepted: %v", err)
			}
		})
	}
}

func TestBranchDecisionConditionDepthBoundary(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{8, 9} {
		t.Run(string(rune('0'+depth)), func(t *testing.T) {
			t.Parallel()
			d, b := stateDecisionFixture(t)
			root := d.Steps[0].If.Conditions
			for i := 1; i < depth; i++ {
				child := root
				root = automations.Condition{
					ID:    automations.ConditionID(string(rune('a' + i))),
					Kind:  automations.ConditionNot,
					Child: &child,
				}
			}
			d.Steps[0].If.Conditions = root
			if depth%2 == 0 {
				b.Outcome = automations.BranchNoMatch
				b.Evaluations[0].Evaluation.Result = automations.ConditionFalse
			}
			err := validateBranchDecision(b, d, nil)
			if depth == 8 && err != nil {
				t.Fatal(err)
			}
			if depth == 9 && !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("excessive depth accepted: %v", err)
			}
		})
	}
}
