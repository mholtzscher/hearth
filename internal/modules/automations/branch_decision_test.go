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
	d.Triggers = append(
		d.Triggers,
		automations.Trigger{ID: "b", Body: automations.CronTrigger{Expression: "* * * * *"}},
	)
	c := d.Steps[0]
	d.Steps = []automations.Step{{ID: "route", Body: automations.ChooseStep{
		Branches: []automations.ChooseBranch{
			{ID: "first", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "first", 1)},
			{ID: "second", Conditions: triggerPredicate("b", "a"), Steps: commandSequence(c, "second", 1)},
		},
	}}}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	decision := automations.BranchDecision{
		StepID: "route", EvaluatedAt: at,
		Body: automations.ChooseDecision{Result: automations.ChooseSelected{BranchID: "second",
			Evaluations: []automations.ChooseEvaluation{
				triggerEvaluation("first", at, automations.ConditionFalse),
				triggerEvaluation("second", at, automations.ConditionTrue, "b"),
			}}},
	}
	return d, decision
}

func triggerEvaluation(
	id automations.BranchID,
	at time.Time,
	result automations.ConditionResult,
	ids ...automations.TriggerID,
) automations.ChooseEvaluation {
	if ids == nil {
		ids = []automations.TriggerID{}
	}
	return automations.ChooseEvaluation{
		BranchID: id, Evaluation: automations.ConditionEvaluation{
			EvaluatedAt: at, Result: result, Nodes: []automations.ConditionNodeResult{
				{ID: "match", Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: ids}},
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
	child := d.Steps[0].Body.(automations.ChooseStep).Branches[0].Conditions
	chooseBody := d.Steps[0].Body.(automations.ChooseStep)
	chooseBody.Branches[0].Conditions = automations.Condition{
		ID:   "negate",
		Body: automations.NotCondition{Child: child},
	}
	d.Steps[0].Body = chooseBody
	chooseEvaluations(b)[0].Evaluation.Nodes[0] = automations.ConditionNodeResult{
		ID: "match", Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}},
	}
	chooseEvaluations(b)[1].Evaluation.Nodes[0].Evidence = automations.TriggerMatchEvidence{
		MatchedTriggerIDs: []automations.TriggerID{"b", "a"},
	}
	matches := []automations.TriggerID{"a", "b"}
	if err := validateBranchDecision(b, d, matches); err != nil {
		t.Fatal(err)
	}
	// The Run's order is not the leaf's configured order [b,a].
	chooseEvaluations(b)[1].Evaluation.Nodes[0].Evidence = automations.TriggerMatchEvidence{
		MatchedTriggerIDs: []automations.TriggerID{"a", "b"},
	}
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
			b.Body = nil
		}},
		{
			"wrong outcome kind",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Body = automations.IfDecision{
					Result: automations.IfSelected{
						Arm:        automations.IfThen,
						Evaluation: chooseEvaluations(*b)[1].Evaluation,
					},
				}
			},
		},
		{
			"omitted selection",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				mutateChooseSelected(b, func(result *automations.ChooseSelected) { result.BranchID = "" })
			},
		},
		{"wrong selection", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			mutateChooseSelected(b, func(result *automations.ChooseSelected) { result.BranchID = "first" })
		}},
		{"time mismatch", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			chooseEvaluations(*b)[0].Evaluation.EvaluatedAt = b.EvaluatedAt.Add(time.Second)
		}},
		{"zero time", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			b.EvaluatedAt = time.Time{}
		}},
		{
			"skipped alternative",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				mutateChooseSelected(
					b,
					func(result *automations.ChooseSelected) { result.Evaluations = result.Evaluations[1:] },
				)
			},
		},
		{
			"extra alternative",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				mutateChooseSelected(b, func(result *automations.ChooseSelected) {
					result.Evaluations = append(
						result.Evaluations,
						triggerEvaluation("third", b.EvaluatedAt, automations.ConditionFalse),
					)
				})
			},
		},
		{"true prefix", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			chooseEvaluations(*b)[0] = triggerEvaluation("first", b.EvaluatedAt, automations.ConditionTrue, "a")
		}},
		{
			"wrong root composition",
			func(d *automations.Definition, _ *automations.BranchDecision, _ *[]automations.TriggerID) {
				child := d.Steps[0].Body.(automations.ChooseStep).Branches[1].Conditions
				chooseBody := d.Steps[0].Body.(automations.ChooseStep)
				chooseBody.Branches[1].Conditions = automations.Condition{
					ID:   "negated",
					Body: automations.NotCondition{Child: child},
				}
				d.Steps[0].Body = chooseBody
			},
		},
		{"wrong leaf ID", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			chooseEvaluations(*b)[1].Evaluation.Nodes[0].ID = "other"
		}},
		{
			"wrong intersection",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				chooseEvaluations(*b)[1].Evaluation.Nodes[0].Evidence = automations.TriggerMatchEvidence{
					MatchedTriggerIDs: []automations.TriggerID{"a"},
				}
			},
		},
		{"missing leaf", func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
			chooseEvaluations(*b)[1].Evaluation.Nodes = nil
		}},
		{
			"Trigger State family mismatch",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				chooseEvaluations(*b)[1].Evaluation.Nodes[0].Evidence = automations.UnknownStateEvidence{
					Reason: automations.ConditionUnknownStateMissing,
				}
			},
		},
		{
			"success with error result",
			func(_ *automations.Definition, b *automations.BranchDecision, _ *[]automations.TriggerID) {
				b.Body = automations.ChooseDecision{
					Result: automations.ChooseError{
						FailureCode: "branch_condition_unknown",
						Evaluations: chooseEvaluations(*b),
					},
				}
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
	const evaluation = `{"evaluated_at":"2026-10-03T12:00:00Z","result":"true","nodes":[{"id":"match","kind":"trigger","result":"true","matched_trigger_ids":["b"]}]}`
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
		{"null nodes", strings.Replace(valid, `[{"id":"match","kind":"trigger","result":"true","matched_trigger_ids":["b"]}]`, `null`, 1)},
		{"legacy Trigger", strings.Replace(valid, `"matched_trigger_ids":["b"]`, `"trigger":{"matched_trigger_ids":["b"]}`, 1)},
		{"missing intersection", strings.Replace(valid, `"matched_trigger_ids":["b"]`, ``, 1)},
		{"null intersection", strings.Replace(valid, `"matched_trigger_ids":["b"]`, `"matched_trigger_ids":null`, 1)},
		{"unknown Trigger field", strings.Replace(valid, `"matched_trigger_ids":["b"]`, `"matched_trigger_ids":["b"],"state":true`, 1)},
		{"missing leaf result", strings.Replace(valid, `"kind":"trigger","result":"true"`, `"kind":"trigger"`, 1)},
		{"missing leaf kind", strings.Replace(valid, `"kind":"trigger",`, ``, 1)},
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
			{ID: "choose", Body: automations.ChooseStep{
				Branches: []automations.ChooseBranch{
					{ID: "one", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "one", 1)},
					{ID: "two", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "two", 1)},
				}, Default: commandSequence(c, "default", 1),
			}},
		}),
		commandSequence(c, "after", 1)[0],
	}
	ifBody := d.Steps[0].Body.(automations.IfStep)
	ifBody.Else = commandSequence(c, "else", 1)
	d.Steps[0].Body = ifBody
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
				automations.ManualCause{},
				nil,
				automations.NotConfiguredDecision(),
				time.Now(),
			)
			if len(run.Steps) != len(tc.ids) || run.BranchDecisions == nil || len(run.BranchDecisions) != 0 {
				t.Fatalf("Run attempts/decisions = %#v / %#v", run.Steps, run.BranchDecisions)
			}
			for i, id := range tc.ids {
				want := automations.StepAttempt{Position: i, StepID: id, State: automations.NotAttemptedStep{}}
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
	ifBody := d.Steps[0].Body.(automations.IfStep)
	ifBody.Conditions = automations.Condition{ID: "state", Body: automations.EntityStateCondition{
		EntityID: c.Body.(automations.CommandStep).EntityID,
		Operator: automations.ComparisonEqual,
		Operand:  json.RawMessage(`null`),
	}}
	d.Steps[0].Body = ifBody

	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return d, automations.BranchDecision{
		StepID:      "state-if",
		EvaluatedAt: at,
		Body: automations.IfDecision{
			Result: automations.IfSelected{Arm: automations.IfThen, Evaluation: automations.ConditionEvaluation{
				EvaluatedAt: at, Result: automations.ConditionTrue, Nodes: []automations.ConditionNodeResult{
					{
						ID: "state",
						Evidence: automations.KnownStateEvidence{
							Matched:       true,
							SelectedValue: json.RawMessage(`null`),
							Observation: automations.ObservationEvidence{
								ObservationID: devices.ObservationID("obs_01950000-0000-7000-8000-000000000001"),
								ObservedAt:    at,
							},
						},
					},
				},
			}},
		},
	}
}

func TestBranchDecisionPreservesSelectedJSONNullAndPrecision(t *testing.T) {
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
	for _, selection := range []string{"9007199254740993", "0.300000000000000000001"} {
		mutateKnownState(
			&decision,
			func(evidence *automations.KnownStateEvidence) { evidence.SelectedValue = json.RawMessage(selection) },
		)
		raw, err = automations.EncodeBranchDecision(decision)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err = automations.DecodeBranchDecision(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(
			ifEvaluation(decoded).Nodes[0].Evidence.(automations.KnownStateEvidence).SelectedValue,
		); got != selection {
			t.Fatalf("retained numeric selection = %s, want %s", got, selection)
		}
	}
	mutateKnownState(&decision, func(evidence *automations.KnownStateEvidence) { evidence.SelectedValue = nil })
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
			prefix := chooseEvaluations(b)
			prefix[1] = triggerEvaluation("second", b.EvaluatedAt, automations.ConditionFalse)
			command := d.Steps[0].Body.(automations.ChooseStep).Branches[0].Steps[0]
			if tc.kind == automations.StepKindIf {
				d.Steps = []automations.Step{ifNode("route", []automations.Step{command})}
				prefix = prefix[:1]
				if tc.fallback {
					ifBody := d.Steps[0].Body.(automations.IfStep)
					ifBody.Else = commandSequence(command, "fallback", 1)
					d.Steps[0].Body = ifBody
				}
			} else if tc.fallback {
				chooseBody := d.Steps[0].Body.(automations.ChooseStep)
				chooseBody.Default = commandSequence(command, "fallback", 1)
				d.Steps[0].Body = chooseBody
			}
			if tc.outcome == automations.BranchUnknown {
				state := automations.Condition{ID: "state", Body: automations.EntityStateCondition{
					EntityID: command.Body.(automations.CommandStep).EntityID,
					Operator: automations.ComparisonEqual,
					Operand:  json.RawMessage(`true`),
				}}
				if tc.kind == automations.StepKindIf {
					ifBody2 := d.Steps[0].Body.(automations.IfStep)
					ifBody2.Conditions = state
					d.Steps[0].Body = ifBody2
				} else {
					chooseBody2 := d.Steps[0].Body.(automations.ChooseStep)
					chooseBody2.Branches[1].Conditions = state
					d.Steps[0].Body = chooseBody2
				}
				last := &prefix[len(prefix)-1].Evaluation
				last.Result = automations.ConditionUnknown
				last.Nodes = []automations.ConditionNodeResult{
					{
						ID:       "state",
						Evidence: automations.UnknownStateEvidence{Reason: automations.ConditionUnknownStateMissing},
					},
				}
			}
			code := ""
			if tc.outcome == automations.BranchError {
				prefix = prefix[:0]
				code = "branch_state_read_failed"
				if tc.kind == automations.StepKindChoose {
					code = "branch_snapshot_incomplete"
				}
				if tc.errorPrefix {
					prefix = []automations.ChooseEvaluation{
						triggerEvaluation("first", b.EvaluatedAt, automations.ConditionFalse),
					}
					code = "branch_state_corrupt"
				}
			}
			if tc.kind == automations.StepKindIf {
				switch tc.outcome {
				case automations.BranchUnknown:
					b.Body = automations.IfDecision{Result: automations.IfUnknown{Evaluation: prefix[0].Evaluation}}
				case automations.BranchError:
					b.Body = automations.IfDecision{Result: automations.IfError{FailureCode: code}}
				case automations.BranchThen,
					automations.BranchElse,
					automations.BranchChosen,
					automations.BranchDefault,
					automations.BranchNoMatch:
					b.Body = automations.IfDecision{
						Result: automations.IfSelected{
							Arm:        automations.IfArm(tc.outcome),
							Evaluation: prefix[0].Evaluation,
						},
					}
				}
			} else {
				switch tc.outcome {
				case automations.BranchUnknown:
					b.Body = automations.ChooseDecision{Result: automations.ChooseUnknown{Evaluations: prefix}}
				case automations.BranchError:
					b.Body = automations.ChooseDecision{
						Result: automations.ChooseError{FailureCode: code, Evaluations: prefix},
					}
				case automations.BranchThen,
					automations.BranchElse,
					automations.BranchChosen,
					automations.BranchDefault,
					automations.BranchNoMatch:
					b.Body = automations.ChooseDecision{
						Result: automations.ChooseFallback{
							Arm:         automations.ChooseFallbackArm(tc.outcome),
							Evaluations: prefix,
						},
					}
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
					ifBody3 := d.Steps[0].Body.(automations.IfStep)
					ifBody3.Else = commandSequence(command, "fallback", 1)
					d.Steps[0].Body = ifBody3
				} else {
					chooseBody3 := d.Steps[0].Body.(automations.ChooseStep)
					chooseBody3.Default = commandSequence(command, "fallback", 1)
					d.Steps[0].Body = chooseBody3
				}
			case automations.BranchElse:
				ifBody4 := d.Steps[0].Body.(automations.IfStep)
				ifBody4.Else = nil
				d.Steps[0].Body = ifBody4
			case automations.BranchDefault:
				chooseBody4 := d.Steps[0].Body.(automations.ChooseStep)
				chooseBody4.Default = nil
				d.Steps[0].Body = chooseBody4
			case automations.BranchThen, automations.BranchChosen, automations.BranchUnknown, automations.BranchError:
				if tc.outcome == automations.BranchUnknown {
					bad := strings.Replace(string(raw), "branch_condition_unknown", "wrong_failure", 1)
					if _, err = automations.DecodeBranchDecision(
						json.RawMessage(bad),
					); !errors.Is(
						err,
						automations.ErrInvalidAutomation,
					) {
						t.Fatalf("wrong unknown failure decoded: %v", err)
					}
					return
				}
				if tc.kind == automations.StepKindIf {
					b.Body = automations.IfDecision{Result: automations.IfError{FailureCode: "wrong_failure"}}
				} else {
					b.Body = automations.ChooseDecision{
						Result: automations.ChooseError{FailureCode: "wrong_failure", Evaluations: prefix},
					}
				}
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
		{"known root with unknown evidence", func(_ *automations.Definition, b *automations.BranchDecision) {
			ifEvaluation(*b).Nodes[0].Evidence = automations.UnknownStateEvidence{Reason: automations.ConditionUnknownStateMissing}
		}},
		{"missing Observation", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) {
				evidence.Observation = automations.ObservationEvidence{}
			})
		}},
		{"bad Observation", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) {
				evidence.Observation.ObservationID = devices.ObservationID("bad")
			})
		}},
		{"zero Observation time", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) { evidence.Observation.ObservedAt = time.Time{} })
		}},
		{"trailing selection", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) { evidence.SelectedValue = json.RawMessage(`null true`) })
		}},
		{"wrong leaf family", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateIfEvaluation(b, func(evaluation *automations.ConditionEvaluation) {
				evaluation.Nodes = []automations.ConditionNodeResult{{ID: "state", Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}}}}
			})
		}},
		{"leaf bound", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateIfEvaluation(b, func(evaluation *automations.ConditionEvaluation) {
				evaluation.Nodes = make([]automations.ConditionNodeResult, 65)
			})
		}},
		{"cyclic Condition", func(d *automations.Definition, _ *automations.BranchDecision) {
			children := make([]automations.Condition, 1)
			children[0] = automations.Condition{ID: "cycle", Body: automations.AllCondition{Children: children}}
			body := d.Steps[0].Body.(automations.IfStep)
			body.Conditions = children[0]
			d.Steps[0].Body = body
		}},
		{"cyclic Step", func(d *automations.Definition, _ *automations.BranchDecision) {
			ifBody := d.Steps[0].Body.(automations.IfStep)
			ifBody.Then = d.Steps
			d.Steps[0].Body = ifBody
		}},
		{"expired known evidence", func(d *automations.Definition, b *automations.BranchDecision) {
			ifBody2 := d.Steps[0].Body.(automations.IfStep)
			stateBody := ifBody2.Conditions.Body.(automations.EntityStateCondition)
			stateBody.MaxAgeSeconds = new(int64(1))
			ifBody2.Conditions.Body = stateBody
			d.Steps[0].Body = ifBody2
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) {
				evidence.Observation.ObservedAt = b.EvaluatedAt.Add(-2 * time.Second)
			})
		}},
		{"incompatible known selection", func(_ *automations.Definition, b *automations.BranchDecision) {
			mutateKnownState(b, func(evidence *automations.KnownStateEvidence) { evidence.SelectedValue = json.RawMessage(`true`) })
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
			evidence := ifEvaluation(*b).Nodes[0].Evidence.(automations.UnknownStateEvidence)
			evidence.Reason = ""
			ifEvaluation(*b).Nodes[0].Evidence = evidence
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
	evaluation := ifEvaluation(*b)
	evidence := evaluation.Nodes[0].Evidence.(automations.KnownStateEvidence)
	evaluation.Result = automations.ConditionUnknown
	evaluation.Nodes[0].Evidence = automations.UnknownStateEvidence{
		Reason:        reason,
		Observation:   &evidence.Observation,
		SelectedValue: evidence.SelectedValue,
	}
	b.Body = automations.IfDecision{Result: automations.IfUnknown{Evaluation: evaluation}}
}

func TestAdmissionDecisionCodecStillRejectsTriggerEvidence(t *testing.T) {
	t.Parallel()
	d, b := stateDecisionFixture(t)
	root := d.Steps[0].Body.(automations.IfStep).Conditions
	evaluation := ifEvaluation(b)
	evaluation.Nodes = []automations.ConditionNodeResult{
		{
			ID:       "state",
			Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}},
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
	const raw = `{"mode":"evaluated","bypass_requested":false,"snapshot":{"id":"state","kind":"entity_state","entity_id":"ent_01950000-0000-7000-8000-000000000001","value_pointer":"","operator":"eq","operand":true},"evaluation":{"evaluated_at":"2026-10-03T12:00:00Z","result":"true","nodes":[{"id":"state","kind":"trigger","result":"true","matched_trigger_ids":["a"]}]}}`
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
			state := d.Steps[0].Body.(automations.IfStep).Conditions
			ifBody := d.Steps[0].Body.(automations.IfStep)
			ifBody.Conditions = automations.Condition{
				ID:   "group",
				Body: groupBody(tc.kind, []automations.Condition{triggerPredicate("a"), state}),
			}
			d.Steps[0].Body = ifBody

			evaluation := ifEvaluation(b)
			evaluation.Result = tc.rootResult
			evaluation.Nodes = []automations.ConditionNodeResult{
				{
					ID:       "match",
					Evidence: automations.TriggerMatchEvidence{MatchedTriggerIDs: tc.matches},
				},
				{
					ID:       "state",
					Evidence: automations.UnknownStateEvidence{Reason: automations.ConditionUnknownStateMissing},
				},
			}
			b.Body = automations.IfDecision{
				Result: automations.IfSelected{Arm: automations.IfArm(tc.outcome), Evaluation: evaluation},
			}
			if err := validateBranchDecision(b, d, tc.matches); err != nil {
				t.Fatal(err)
			}
			nodes := ifEvaluation(b).Nodes
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
			root := d.Steps[0].Body.(automations.IfStep).Conditions
			for i := 1; i < depth; i++ {
				child := root
				root = automations.Condition{
					ID:   automations.ConditionID(string(rune('a' + i))),
					Body: automations.NotCondition{Child: child},
				}
			}
			ifBody := d.Steps[0].Body.(automations.IfStep)
			ifBody.Conditions = root
			d.Steps[0].Body = ifBody
			if depth%2 == 0 {
				evaluation := ifEvaluation(b)
				evaluation.Result = automations.ConditionFalse
				b.Body = automations.IfDecision{
					Result: automations.IfSelected{Arm: automations.IfNoMatch, Evaluation: evaluation},
				}
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

func groupBody(kind automations.ConditionKind, children []automations.Condition) automations.ConditionBody {
	if kind == automations.ConditionAll {
		return automations.AllCondition{Children: children}
	}
	return automations.AnyCondition{Children: children}
}

type unsupportedBranchBody struct{ automations.BranchDecisionBody }
type unsupportedIfResult struct{ automations.IfDecisionResult }
type unsupportedChooseResult struct {
	automations.ChooseDecisionResult
}
type unsupportedConditionEvidence struct{ automations.ConditionEvidence }

// Arbitrary repository input and retained encoding both reject pointer and
// embedded-interface implementations rather than trusting marker satisfaction.
func TestBranchDecisionRejectsUnsupportedValues(t *testing.T) {
	t.Parallel()
	d, valid := stateDecisionFixture(t)
	ifBody := valid.Body.(automations.IfDecision)
	chooseBody := automations.ChooseDecision{Result: automations.ChooseError{FailureCode: "branch_state_read_failed"}}
	known := ifEvaluation(valid)
	unknown := automations.ConditionEvaluation{
		EvaluatedAt: valid.EvaluatedAt,
		Result:      automations.ConditionUnknown,
		Nodes: []automations.ConditionNodeResult{
			{ID: "state", Evidence: automations.UnknownStateEvidence{Reason: automations.ConditionUnknownStateMissing}},
		},
	}
	falseRoot := automations.ConditionEvaluation{EvaluatedAt: valid.EvaluatedAt, Result: automations.ConditionFalse,
		Nodes: []automations.ConditionNodeResult{{ID: "match", Evidence: automations.TriggerMatchEvidence{}}}}
	cases := []struct {
		name string
		body automations.BranchDecisionBody
	}{
		{"nil body", nil},
		{"If pointer", &ifBody},
		{"nil If pointer", (*automations.IfDecision)(nil)},
		{"Choose pointer", &chooseBody},
		{"nil Choose pointer", (*automations.ChooseDecision)(nil)},
		{"embedded body", unsupportedBranchBody{valid.Body}},
		{"nil If result", automations.IfDecision{}},
		{
			"selected If pointer",
			automations.IfDecision{Result: &automations.IfSelected{Arm: automations.IfThen, Evaluation: known}},
		},
		{"unknown If pointer", automations.IfDecision{Result: &automations.IfUnknown{Evaluation: unknown}}},
		{
			"error If pointer",
			automations.IfDecision{Result: &automations.IfError{FailureCode: "branch_state_read_failed"}},
		},
		{"typed nil If result", automations.IfDecision{Result: (*automations.IfSelected)(nil)}},
		{"embedded If result", automations.IfDecision{Result: unsupportedIfResult{ifBody.Result}}},
		{"nil Choose result", automations.ChooseDecision{}},
		{
			"selected Choose pointer",
			automations.ChooseDecision{
				Result: &automations.ChooseSelected{
					BranchID:    "a",
					Evaluations: []automations.ChooseEvaluation{{BranchID: "a", Evaluation: known}},
				},
			},
		},
		{
			"fallback Choose pointer",
			automations.ChooseDecision{
				Result: &automations.ChooseFallback{
					Arm:         automations.ChooseNoMatch,
					Evaluations: []automations.ChooseEvaluation{{BranchID: "a", Evaluation: falseRoot}},
				},
			},
		},
		{
			"unknown Choose pointer",
			automations.ChooseDecision{
				Result: &automations.ChooseUnknown{
					Evaluations: []automations.ChooseEvaluation{{BranchID: "a", Evaluation: unknown}},
				},
			},
		},
		{
			"error Choose pointer",
			automations.ChooseDecision{Result: &automations.ChooseError{FailureCode: "branch_state_read_failed"}},
		},
		{"typed nil Choose result", automations.ChooseDecision{Result: (*automations.ChooseError)(nil)}},
		{"embedded Choose result", automations.ChooseDecision{Result: unsupportedChooseResult{chooseBody.Result}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := valid
			decision.Body = tc.body
			if _, err := automations.EncodeBranchDecision(decision); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("encoded %T: %v", tc.body, err)
			}
			if err := validateBranchDecision(decision, d, nil); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("validated %T: %v", tc.body, err)
			}
		})
	}
}

func TestConditionEvidenceBoundariesRejectUnsupportedValues(t *testing.T) {
	t.Parallel()
	_, valid := stateDecisionFixture(t)
	known := ifEvaluation(valid).Nodes[0].Evidence.(automations.KnownStateEvidence)
	unknown := automations.UnknownStateEvidence{Reason: automations.ConditionUnknownStateMissing}
	trigger := automations.TriggerMatchEvidence{MatchedTriggerIDs: []automations.TriggerID{"a"}}
	for _, evidence := range []automations.ConditionEvidence{
		nil, &known, (*automations.KnownStateEvidence)(nil),
		&unknown, (*automations.UnknownStateEvidence)(nil),
		&trigger, (*automations.TriggerMatchEvidence)(nil),
		unsupportedConditionEvidence{known},
	} {
		d, decision := stateDecisionFixture(t)
		evaluation := ifEvaluation(decision)
		evaluation.Nodes[0].Evidence = evidence
		root := d.Steps[0].Body.(automations.IfStep).Conditions
		if _, err := automations.EncodeConditionDecision(
			automations.EvaluatedDecision(root, evaluation),
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("admission encoded %T: %v", evidence, err)
		}
		if _, err := automations.EncodeBranchDecision(decision); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("branch encoded %T: %v", evidence, err)
		}
		if err := validateBranchDecision(decision, d, nil); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("repository validated %T: %v", evidence, err)
		}
	}
}
