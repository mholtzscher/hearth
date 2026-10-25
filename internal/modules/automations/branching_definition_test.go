package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func branchingFixture(t *testing.T) automations.Definition {
	t.Helper()
	d := validDomainDefinition(t)
	d.Triggers = []automations.Trigger{
		{ID: "a", Body: automations.CronTrigger{Expression: "* * * * *"}},
	}
	return d
}

func triggerPredicate(ids ...automations.TriggerID) automations.Condition {
	return automations.Condition{ID: "match", Body: automations.TriggerCondition{TriggerIDs: ids}}
}

func commandSequence(command automations.Step, prefix string, count int) []automations.Step {
	steps := make([]automations.Step, count)
	for i := range steps {
		steps[i] = command
		steps[i].ID = automations.StepID(fmt.Sprintf("%s%d", prefix, i))
	}
	return steps
}

func ifNode(id string, then []automations.Step) automations.Step {
	return automations.Step{
		ID:   automations.StepID(id),
		Body: automations.IfStep{Conditions: triggerPredicate("a"), Then: then},
	}
}

// wireDefinition builds test input independently of the production encoder, so
// the decoder can be exercised with structures that typed normalization rejects.
//
//nolint:gocognit // Independent recursive wire fixture covers each public family.
func wireDefinition(t *testing.T, d automations.Definition) json.RawMessage {
	t.Helper()
	var condition func(automations.Condition) any
	condition = func(c automations.Condition) any {
		m := map[string]any{"id": c.ID, "kind": c.Kind()}
		switch body := c.Body.(type) {
		case automations.ExpressionCondition:
			m["value"] = body.Value
		case automations.TriggerCondition:
			m["trigger_ids"] = body.TriggerIDs
		case automations.EntityStateCondition:
			m["entity_id"], m["value_pointer"], m["operator"], m["operand"] = c.Body.(automations.EntityStateCondition).EntityID, c.Body.(automations.EntityStateCondition).Pointer, c.Body.(automations.EntityStateCondition).Operator, c.Body.(automations.EntityStateCondition).Operand
		case automations.AllCondition:
			children := make([]any, 0, len(body.Children))
			for _, child := range body.Children {
				children = append(children, condition(child))
			}
			m["children"] = children
		case automations.AnyCondition:
			children := make([]any, 0, len(body.Children))
			for _, child := range body.Children {
				children = append(children, condition(child))
			}
			m["children"] = children
		case automations.NotCondition:
			m["child"] = condition(c.Body.(automations.NotCondition).Child)
		}
		return m
	}
	var sequence func([]automations.Step) []any
	sequence = func(steps []automations.Step) []any {
		out := make([]any, 0, len(steps))
		for _, s := range steps {
			m := map[string]any{"id": s.ID, "kind": s.Kind()}
			switch s.Kind() {
			case "", automations.StepKindCommand:
				m["entity_id"], m["operation"], m["parameters"] = s.Body.(automations.CommandStep).EntityID, s.Body.(automations.CommandStep).OperationName, s.Body.(automations.CommandStep).Parameters
			case automations.StepKindIf:
				m["kind"], m["conditions"], m["then"] = s.Kind(), condition(
					s.Body.(automations.IfStep).Conditions,
				), sequence(
					s.Body.(automations.IfStep).Then,
				)
				if s.Body.(automations.IfStep).Else != nil {
					m["else"] = sequence(s.Body.(automations.IfStep).Else)
				}
			case automations.StepKindChoose:
				m["kind"] = s.Kind()
				branches := make([]any, 0, len(s.Body.(automations.ChooseStep).Branches))
				for _, b := range s.Body.(automations.ChooseStep).Branches {
					branches = append(
						branches,
						map[string]any{"id": b.ID, "conditions": condition(b.Conditions), "steps": sequence(b.Steps)},
					)
				}
				m["branches"] = branches
				if s.Body.(automations.ChooseStep).Default != nil {
					m["default"] = sequence(s.Body.(automations.ChooseStep).Default)
				}
			case automations.StepKindNotification:
				body := s.Body.(automations.NotificationStep)
				m["kind"], m["destination_id"], m["title"], m["message"] = s.Kind(), body.DestinationID, body.Title, body.Message
			case automations.StepKindDelay:
				m["kind"], m["duration_ms"] = s.Kind(), map[string]int64{
					"literal": s.Body.(automations.DelayStep).DurationMS,
				}
			case automations.StepKindFunction:
				body := s.Body.(automations.FunctionStep)
				m["adapter_id"], m["function_key"] = body.AdapterID, body.FunctionKey
				m["contract_fingerprint"], m["arguments"] = body.ContractFingerprint, body.Arguments
			case automations.StepKindSetVariables:
				m["assignments"] = s.Body.(automations.SetVariablesStep).Assignments
			case automations.StepKindReadState:
				body := s.Body.(automations.ReadStateStep)
				m["entity_id"], m["value_pointer"], m["on_unusable"] = body.EntityID, body.Pointer, body.OnUnusable
				if body.MaxAgeSeconds != nil {
					m["max_age_seconds"] = *body.MaxAgeSeconds
				}
			}
			out = append(out, m)
		}
		return out
	}
	triggers := make([]any, 0, len(d.Triggers))
	for _, trigger := range d.Triggers {
		triggers = append(
			triggers,
			map[string]any{
				"id":         trigger.ID,
				"kind":       trigger.Kind(),
				"expression": trigger.Body.(automations.CronTrigger).Expression,
			},
		)
	}
	m := map[string]any{"name": d.Name, "enabled": d.Enabled, "triggers": triggers, "steps": sequence(d.Steps)}
	if d.Variables != nil {
		m["variables"] = d.Variables
	}
	if d.Conditions != nil {
		m["conditions"] = condition(*d.Conditions)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBranchingWireAndStableLeafOrder(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = []automations.Step{
		ifNode("outer", []automations.Step{c}),
		{ID: "choose", Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
			{
				ID:         "first",
				Conditions: triggerPredicate("a"),
				Steps:      []automations.Step{ifNode("inner", commandSequence(c, "nested", 1))},
			},
			{ID: "second", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "alternative", 1)},
		}, Default: commandSequence(c, "default", 1)}},
	}
	ifBody := d.Steps[0].Body.(automations.IfStep)
	ifBody.Else = commandSequence(c, "else", 1)
	d.Steps[0].Body = ifBody
	normalized, raw, err := automations.NormalizeAndEncodeDefinition(d)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]automations.StepID, 0)
	for _, leaf := range automations.CommandLeaves(decoded.Steps) {
		ids = append(ids, leaf.ID)
	}
	if want := []automations.StepID{
		c.ID,
		"else0",
		"nested0",
		"alternative0",
		"default0",
	}; !reflect.DeepEqual(
		ids,
		want,
	) {
		t.Fatalf("leaves = %v, want %v", ids, want)
	}
	if !bytes.Contains(raw, []byte(`"kind":"command"`)) {
		t.Fatalf("command shape changed: %s", raw)
	}
	before := bytes.Clone(raw)
	ifBody2 := d.Steps[0].Body.(automations.IfStep)
	predicate := ifBody2.Conditions.Body.(automations.TriggerCondition)
	predicate.TriggerIDs[0] = "missing"
	ifBody2.Conditions.Body = predicate
	d.Steps[0].Body = ifBody2
	ifBody3 := d.Steps[0].Body.(automations.IfStep)
	commandBody := ifBody3.Then[0].Body.(automations.CommandStep)
	literalBytes(commandBody.Parameters)[0] = ' '
	ifBody3.Then[0].Body = commandBody
	d.Steps[0].Body = ifBody3
	after, err := automations.EncodeDefinition(normalized)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("normalized tree aliases input: %s, %v", after, err)
	}
}

// JSON encoding expands literal HTML characters even without legacy aliases.
// The decoder must bound the normalized representation as well as input bytes.
func TestBranchingDecodeBoundsNormalizedBytesAfterHTMLEscaping(t *testing.T) {
	t.Parallel()
	definition := branchingFixture(t)
	command := definition.Steps[0].Body.(automations.CommandStep)
	command.Parameters = literalJSON(`{"value":"` + strings.Repeat("<", 24000) + `"}`)
	definition.Steps[0].Body = command
	definition.Steps = []automations.Step{ifNode("if", definition.Steps)}
	raw := strings.ReplaceAll(string(wireDefinition(t, definition)), `\u003c`, "<")
	if len(raw) >= 65536 {
		t.Fatalf("input fixture exceeds raw bound: %d", len(raw))
	}
	if _, err := automations.DecodeDefinition(json.RawMessage(raw)); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("normalized oversized document accepted: %v", err)
	}
}

func TestBranchingRejectsInvalidTypedTreesAndCycles(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*automations.Definition){
		"empty choose alternatives": func(d *automations.Definition) {
			d.Steps = []automations.Step{
				{ID: "choose", Body: automations.ChooseStep{}},
			}
		},
		"missing if payload": func(d *automations.Definition) {
			d.Steps = []automations.Step{{ID: "if", Body: nil}}
		},
		"typed nil Command": func(d *automations.Definition) { d.Steps[0].Body = (*automations.CommandStep)(nil) },
		"foreign Step":      func(d *automations.Definition) { d.Steps[0].Body = unsupportedStepBody{} },
		"duplicate branch IDs": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{
				{ID: "choose", Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
					{ID: "same", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "first", 1)},
					{ID: "same", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "second", 1)},
				}}},
			}
		},
		"foreign command payload": func(d *automations.Definition) { d.Steps[0].Body = &automations.IfStep{} },
		"foreign branch fields": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", []automations.Step{c})}
			d.Steps[0].Body = &automations.CommandStep{}
		},
		"global duplicate": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", []automations.Step{c})}
			ifBody := d.Steps[0].Body.(automations.IfStep)
			ifBody.Else = []automations.Step{c}
			d.Steps[0].Body = ifBody
		},
		"empty optional": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody2 := d.Steps[0].Body.(automations.IfStep)
			ifBody2.Else = []automations.Step{}
			d.Steps[0].Body = ifBody2
		},
		"missing trigger": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody3 := d.Steps[0].Body.(automations.IfStep)
			ifBody3.Conditions = triggerPredicate("missing")
			d.Steps[0].Body = ifBody3
		},
		"duplicate trigger refs": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody4 := d.Steps[0].Body.(automations.IfStep)
			ifBody4.Conditions = triggerPredicate("a", "a")
			d.Steps[0].Body = ifBody4
		},
		"trigger admission": func(d *automations.Definition) {
			root := automations.Condition{ID: "not", Body: automations.NotCondition{Child: automations.Condition{}}}
			child := triggerPredicate("a")
			notBody := root.Body.(automations.NotCondition)
			notBody.Child = child
			root.Body = notBody
			d.Conditions = &root
		},
		"step cycle": func(d *automations.Definition) {
			children := make([]automations.Step, 1)
			s := ifNode("cycle", children)
			children[0] = s
			d.Steps = []automations.Step{s}
		},
		"condition cycle": func(d *automations.Definition) {
			children := make([]automations.Condition, 1)
			root := automations.Condition{ID: "cycle", Body: automations.AllCondition{Children: children}}
			children[0] = root
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			branch := d.Steps[0].Body.(automations.IfStep)
			branch.Conditions = root
			d.Steps[0].Body = branch
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := branchingFixture(t)
			mutate(&d)
			if _, err := automations.NormalizeDefinition(d); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("normalize = %v", err)
			}
			if _, err := automations.EncodeDefinition(d); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("encode = %v", err)
			}
		})
	}
}

type unsupportedStepBody struct{ automations.StepBody }

func TestBranchingStrictJSONVariants(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	d.Steps = []automations.Step{ifNode("if", d.Steps)}
	raw := string(wireDefinition(t, d))
	for name, invalid := range map[string]string{
		"missing command kind":  strings.Replace(raw, `"kind":"command",`, ``, 1),
		"command delay field":   strings.Replace(raw, `"operation":"set"`, `"duration_ms":1,"operation":"set"`, 1),
		"if delay field":        strings.Replace(raw, `"kind":"if"`, `"kind":"if","duration_ms":1`, 1),
		"mixed fields":          strings.Replace(raw, `"kind":"if"`, `"kind":"if","operation":"set"`, 1),
		"unknown":               strings.Replace(raw, `"kind":"if"`, `"kind":"if","extra":true`, 1),
		"null conditions":       strings.Replace(raw, `"conditions":{"id":"match","kind":"trigger","trigger_ids":["a"]}`, `"conditions":null`, 1),
		"empty then":            strings.Replace(raw, `"then":[`, `"then":[],"else":[`, 1),
		"null else":             strings.Replace(raw, `"kind":"if"`, `"kind":"if","else":null`, 1),
		"empty else":            strings.Replace(raw, `"kind":"if"`, `"kind":"if","else":[]`, 1),
		"trigger foreign field": strings.Replace(raw, `"trigger_ids":["a"]`, `"trigger_ids":["a"],"operand":true`, 1),
		"null trigger IDs":      strings.Replace(raw, `"trigger_ids":["a"]`, `"trigger_ids":null`, 1),
		"empty trigger IDs":     strings.Replace(raw, `"trigger_ids":["a"]`, `"trigger_ids":[]`, 1),
		"trailing":              raw + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if invalid == raw {
				t.Fatal("invalid fixture did not change")
			}
			if _, err := automations.DecodeDefinition(
				json.RawMessage(invalid),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("decode = %v", err)
			}
		})
	}
}

func TestChooseStrictJSONAndLocalBranchIDScope(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = nil
	for i := range 2 {
		d.Steps = append(
			d.Steps,
			automations.Step{
				ID: automations.StepID(fmt.Sprintf("choose%d", i)),
				Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
					{
						ID:         "same-local-id",
						Conditions: triggerPredicate("a"),
						Steps:      commandSequence(c, fmt.Sprintf("c%d_", i), 1),
					},
				}},
			},
		)
	}
	raw := wireDefinition(t, d)
	if _, err := automations.DecodeDefinition(raw); err != nil {
		t.Fatalf("branch IDs must be Choose-local: %v", err)
	}
	for name, field := range map[string]string{
		"null default": `"default":null`, "empty default": `"default":[]`,
		"foreign then": `"then":[]`, "foreign conditions": `"conditions":null`,
		"delay duration": `"duration_ms":1`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			invalid := strings.Replace(string(raw), `"kind":"choose"`, `"kind":"choose",`+field, 1)
			if _, err := automations.DecodeDefinition(
				json.RawMessage(invalid),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("decode = %v", err)
			}
		})
	}
}

func TestTriggerConditionsRemainInvalidAtAdmissionBoundaries(t *testing.T) {
	t.Parallel()
	root := triggerPredicate("a")
	if _, err := automations.NormalizeConditions(root); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("normalize = %v", err)
	}
	if _, err := automations.RequiredConditionEntityIDs(root); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("references = %v", err)
	}
	if _, err := automations.EvaluateConditions(
		root,
		devices.EntityStateSnapshot{},
		time.Now(),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("evaluate = %v", err)
	}
	if _, err := automations.EncodeConditionDecision(
		automations.BypassedDecision(root),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("decision encoding = %v", err)
	}
	raw := json.RawMessage(
		`{"mode":"bypassed","bypass_requested":true,"snapshot":{"id":"match","kind":"trigger","trigger_ids":["a"]}}`,
	)
	if _, err := automations.DecodeConditionDecision(raw); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("decision decoding = %v", err)
	}
}

type branchingReferenceDevices struct {
	automations.AutomationDevices

	commandEntity   devices.EntityID
	conditionEntity devices.EntityID
	conditionError  error
	commandError    error
	stateReads      int
}

func (d *branchingReferenceDevices) ValidateCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandParameters, error) {
	if input.EntityID == d.commandEntity && d.commandError != nil {
		return nil, d.commandError
	}
	return input.Parameters, nil
}

func (d *branchingReferenceDevices) ValidateConditionEntity(_ context.Context, id devices.EntityID) error {
	if id == d.conditionEntity {
		return d.conditionError
	}
	return nil
}

func (d *branchingReferenceDevices) GetEntityStateSnapshot(
	context.Context,
	[]devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	d.stateReads++
	return devices.EntityStateSnapshot{}, errors.New("State is unavailable")
}

func TestBranchingSaveValidatesUnreachableReferencesWithoutReadingState(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	c.ID = "unreachable-command"
	commandBody := c.Body.(automations.CommandStep)
	commandBody.EntityID = newEntityID(t)
	c.Body = commandBody
	stateEntity := newEntityID(t)
	nested := ifNode("unreachable-if", []automations.Step{c})
	ifBody := nested.Body.(automations.IfStep)
	ifBody.Conditions = automations.Condition{ID: "state", Body: automations.EntityStateCondition{
		EntityID: stateEntity,
		Operator: automations.ComparisonEqual,
		Operand:  json.RawMessage(`true`),
	}}
	nested.Body = ifBody

	d.Steps = []automations.Step{ifNode("outer", d.Steps)}
	ifBody2 := d.Steps[0].Body.(automations.IfStep)
	ifBody2.Else = []automations.Step{nested}
	d.Steps[0].Body = ifBody2
	for _, arm := range []string{"if else", "choose alternative", "choose default"} {
		t.Run(arm, func(t *testing.T) {
			t.Parallel()
			definition := d
			if arm != "if else" {
				choose := automations.ChooseStep{Branches: []automations.ChooseBranch{
					{
						ID:         "selected",
						Conditions: triggerPredicate("a"),
						Steps:      commandSequence(d.Steps[0].Body.(automations.IfStep).Then[0], "selected", 1),
					},
				}}
				if arm == "choose alternative" {
					choose.Branches = append(
						choose.Branches,
						automations.ChooseBranch{
							ID:         "unreachable",
							Conditions: triggerPredicate("a"),
							Steps:      []automations.Step{nested},
						},
					)
				} else {
					choose.Default = []automations.Step{nested}
				}
				definition.Steps = []automations.Step{{ID: "choose", Body: choose}}
			}
			assertSaveReferenceValidation(t, definition, c.Body.(automations.CommandStep).EntityID, stateEntity)
		})
	}
}

func assertSaveReferenceValidation(
	t *testing.T,
	d automations.Definition,
	commandEntity, stateEntity devices.EntityID,
) {
	t.Helper()
	stub := &branchingReferenceDevices{commandEntity: commandEntity, conditionEntity: stateEntity}
	if _, err := automations.ValidateDefinition(context.Background(), stub, d); err != nil {
		t.Fatalf("valid references without State rejected: %v", err)
	}
	for _, failure := range []string{"missing Entity", "stateless Entity", "unsupported command"} {
		stub.conditionError, stub.commandError = nil, nil
		if failure == "unsupported command" {
			stub.commandError = errors.New(failure)
		} else {
			stub.conditionError = errors.New(failure)
		}
		if _, err := automations.ValidateDefinition(
			context.Background(),
			stub,
			d,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("%s accepted: %v", failure, err)
		}
	}
	if stub.stateReads != 0 {
		t.Fatalf("save read State %d times", stub.stateReads)
	}
}
