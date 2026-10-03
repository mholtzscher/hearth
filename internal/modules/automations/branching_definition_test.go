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
		{ID: "a", Kind: automations.TriggerKindCron, Cron: &automations.CronTrigger{Expression: "* * * * *"}},
	}
	return d
}

func triggerPredicate(ids ...automations.TriggerID) automations.Condition {
	return automations.Condition{
		ID:      "match",
		Kind:    automations.ConditionTrigger,
		Trigger: &automations.TriggerCondition{TriggerIDs: ids},
	}
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
		Kind: automations.StepKindIf,
		If:   &automations.IfStep{Conditions: triggerPredicate("a"), Then: then},
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
		m := map[string]any{"id": c.ID, "kind": c.Kind}
		switch c.Kind {
		case automations.ConditionTrigger:
			m["trigger_ids"] = c.Trigger.TriggerIDs
		case automations.ConditionEntityState:
			m["entity_id"], m["value_pointer"], m["operator"], m["operand"] = c.EntityState.EntityID, c.EntityState.Pointer, c.EntityState.Operator, c.EntityState.Operand
		case automations.ConditionAll, automations.ConditionAny:
			children := make([]any, 0, len(c.Children))
			for _, child := range c.Children {
				children = append(children, condition(child))
			}
			m["children"] = children
		case automations.ConditionNot:
			m["child"] = condition(*c.Child)
		}
		return m
	}
	var sequence func([]automations.Step) []any
	sequence = func(steps []automations.Step) []any {
		out := make([]any, 0, len(steps))
		for _, s := range steps {
			m := map[string]any{"id": s.ID}
			switch s.Kind {
			case "", automations.StepKindCommand:
				m["entity_id"], m["operation"], m["parameters"] = s.EntityID, s.OperationName, json.RawMessage(
					s.Parameters,
				)
			case automations.StepKindIf:
				m["kind"], m["conditions"], m["then"] = s.Kind, condition(s.If.Conditions), sequence(s.If.Then)
				if s.If.Else != nil {
					m["else"] = sequence(s.If.Else)
				}
			case automations.StepKindChoose:
				m["kind"] = s.Kind
				branches := make([]any, 0, len(s.Choose.Branches))
				for _, b := range s.Choose.Branches {
					branches = append(
						branches,
						map[string]any{"id": b.ID, "conditions": condition(b.Conditions), "steps": sequence(b.Steps)},
					)
				}
				m["branches"] = branches
				if s.Choose.Default != nil {
					m["default"] = sequence(s.Choose.Default)
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
			map[string]any{"id": trigger.ID, "kind": trigger.Kind, "expression": trigger.Cron.Expression},
		)
	}
	m := map[string]any{"name": d.Name, "enabled": d.Enabled, "triggers": triggers, "steps": sequence(d.Steps)}
	if d.Conditions != nil {
		m["conditions"] = condition(*d.Conditions)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBranchingLegacyWireAndStableLeafOrder(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = []automations.Step{
		ifNode("outer", []automations.Step{c}),
		{
			ID:   "choose",
			Kind: automations.StepKindChoose,
			Choose: &automations.ChooseStep{Branches: []automations.ChooseBranch{
				{
					ID:         "first",
					Conditions: triggerPredicate("a"),
					Steps:      []automations.Step{ifNode("inner", commandSequence(c, "nested", 1))},
				},
				{ID: "second", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "alternative", 1)},
			}, Default: commandSequence(c, "default", 1)},
		},
	}
	d.Steps[0].If.Else = commandSequence(c, "else", 1)
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
	if bytes.Contains(raw, []byte(`"kind":"command"`)) {
		t.Fatalf("command shape changed: %s", raw)
	}
	before := bytes.Clone(raw)
	d.Steps[0].If.Conditions.Trigger.TriggerIDs[0] = "missing"
	d.Steps[0].If.Then[0].Parameters[0] = ' '
	after, err := automations.EncodeDefinition(normalized)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("normalized tree aliases input: %s, %v", after, err)
	}
}

//nolint:gocognit // Boundary table includes independent fixtures for every aggregate bound.
func TestBranchingBoundsAtDomainAndCodecBoundaries(t *testing.T) {
	t.Parallel()
	wide := func(n int) automations.Condition {
		root := automations.Condition{ID: "root", Kind: automations.ConditionAll}
		for i := range n - 1 {
			leaf := triggerPredicate("a")
			leaf.ID = automations.ConditionID(fmt.Sprintf("leaf%d", i))
			root.Children = append(root.Children, leaf)
		}
		return root
	}
	tests := []struct {
		name  string
		limit int
		build func(automations.Definition, int) automations.Definition
	}{
		{"branch ID bytes", 63, func(d automations.Definition, n int) automations.Definition {
			d.Steps = []automations.Step{
				{
					ID:   "choose",
					Kind: automations.StepKindChoose,
					Choose: &automations.ChooseStep{Branches: []automations.ChooseBranch{
						{
							ID:         automations.BranchID(strings.Repeat("b", n)),
							Conditions: triggerPredicate("a"),
							Steps:      d.Steps,
						},
					}},
				},
			}
			return d
		}},
		{"sequence", 32, func(d automations.Definition, n int) automations.Definition {
			d.Steps = commandSequence(d.Steps[0], "c", n)
			return d
		}},
		{"commands across arms", 32, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", commandSequence(c, "then", 16))}
			d.Steps[0].If.Else = commandSequence(c, "else", n-16)
			return d
		}},
		{"step depth", 8, func(d automations.Definition, n int) automations.Definition {
			step := d.Steps[0]
			for i := 1; i < n; i++ {
				step = ifNode(fmt.Sprintf("if%d", i), []automations.Step{step})
			}
			d.Steps = []automations.Step{step}
			return d
		}},
		{"all steps", 64, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			d.Steps = nil
			for i := range 32 {
				d.Steps = append(d.Steps, ifNode(fmt.Sprintf("if%d", i), commandSequence(c, fmt.Sprintf("c%d_", i), 1)))
			}
			if n > 64 {
				d.Steps[0].If.Then = []automations.Step{ifNode("extra", d.Steps[0].If.Then)}
			}
			return d
		}},
		{"choose alternatives", 32, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			choose := &automations.ChooseStep{}
			for i := range n {
				choose.Branches = append(
					choose.Branches,
					automations.ChooseBranch{
						ID:         automations.BranchID(fmt.Sprintf("b%d", i)),
						Conditions: triggerPredicate("a"),
						Steps:      commandSequence(c, fmt.Sprintf("c%d_", i), 1),
					},
				)
			}
			d.Steps = []automations.Step{{ID: "choose", Kind: automations.StepKindChoose, Choose: choose}}
			return d
		}},
		{"condition root nodes", 64, func(d automations.Definition, n int) automations.Definition {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = wide(n)
			return d
		}},
		{"condition depth", 8, func(d automations.Definition, n int) automations.Definition {
			root := triggerPredicate("a")
			for i := 1; i < n; i++ {
				child := root
				root = automations.Condition{
					ID:    automations.ConditionID(fmt.Sprintf("not%d", i)),
					Kind:  automations.ConditionNot,
					Child: &child,
				}
			}
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = root
			return d
		}},
		{"total conditions", 256, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			d.Steps = nil
			for i := range 4 {
				s := ifNode(fmt.Sprintf("if%d", i), commandSequence(c, fmt.Sprintf("c%d_", i), 1))
				s.If.Conditions = wide(64)
				d.Steps = append(d.Steps, s)
			}
			if n > 256 {
				d.Steps = append(d.Steps, ifNode("extra", commandSequence(c, "extra", 1)))
			}
			return d
		}},
		{"trigger IDs", 32, func(d automations.Definition, n int) automations.Definition {
			ids := make([]automations.TriggerID, n)
			d.Triggers = nil
			for i := range n {
				ids[i] = automations.TriggerID(fmt.Sprintf("t%d", i))
				if i < 32 {
					d.Triggers = append(
						d.Triggers,
						automations.Trigger{
							ID:   ids[i],
							Kind: automations.TriggerKindCron,
							Cron: &automations.CronTrigger{Expression: "* * * * *"},
						},
					)
				}
			}
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = triggerPredicate(ids...)
			return d
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			for _, n := range []int{tc.limit, tc.limit + 1} {
				d := tc.build(branchingFixture(t), n)
				_, domainErr := automations.NormalizeDefinition(d)
				_, codecErr := automations.DecodeDefinition(wireDefinition(t, d))
				_, repositoryErr := repository.CreateAutomation(context.Background(), d)
				for boundary, err := range map[string]error{"domain": domainErr, "codec": codecErr, "repository": repositoryErr} {
					if n == tc.limit && err != nil {
						t.Fatalf("%s limit rejected: %v", boundary, err)
					}
					if n > tc.limit && !errors.Is(err, automations.ErrInvalidAutomation) {
						t.Fatalf("%s one beyond = %v", boundary, err)
					}
				}
			}
		})
	}
}

func TestBranchingNormalizedBytesLimitAndOneBeyond(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	d.Steps = []automations.Step{ifNode("if", d.Steps)}
	d.Steps[0].If.Then[0].Parameters = devices.CommandParameters(`{"value":""}`)
	baseSize := len(wireDefinition(t, d))
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	for _, size := range []int{65536, 65537} {
		d.Steps[0].If.Then[0].Parameters = devices.CommandParameters(
			`{"value":"` + strings.Repeat("x", size-baseSize) + `"}`,
		)
		raw := wireDefinition(t, d)
		if len(raw) != size {
			t.Fatalf("fixture bytes = %d, want %d", len(raw), size)
		}
		_, normalized, domainErr := automations.NormalizeAndEncodeDefinition(d)
		_, codecErr := automations.DecodeDefinition(raw)
		_, repositoryErr := repository.CreateAutomation(context.Background(), d)
		for boundary, err := range map[string]error{"domain": domainErr, "codec": codecErr, "repository": repositoryErr} {
			if size == 65536 && err != nil {
				t.Fatalf("%s limit rejected: %v", boundary, err)
			}
			if size == 65537 && !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("%s one beyond = %v", boundary, err)
			}
		}
		if size == 65536 && len(normalized) != size {
			t.Fatalf("normalized bytes = %d", len(normalized))
		}
	}
}

func TestBranchingDecodeBoundsNormalizedBytesAfterLegacyAliasExpansion(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	d.Steps = []automations.Step{ifNode("if", d.Steps)}
	d.Steps[0].If.Conditions = automations.Condition{
		ID:   "state",
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: newEntityID(t),
			Operator: automations.ComparisonEqual,
			Operand:  json.RawMessage(`true`),
		},
	}
	d.Steps[0].If.Then[0].Parameters = devices.CommandParameters(`{"value":""}`)
	baseSize := len(wireDefinition(t, d))
	d.Steps[0].If.Then[0].Parameters = devices.CommandParameters(
		`{"value":"` + strings.Repeat("x", 65537-baseSize) + `"}`,
	)
	raw := strings.Replace(string(wireDefinition(t, d)), `"value_pointer"`, `"pointer"`, 1)
	if len(raw) >= 65536 {
		t.Fatalf("legacy fixture must fit the raw byte limit, got %d", len(raw))
	}
	if _, err := automations.DecodeDefinition(json.RawMessage(raw)); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("normalized oversized document accepted: %v", err)
	}
}

func TestBranchingRejectsInvalidTypedTreesAndCycles(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*automations.Definition){
		"zero commands": func(d *automations.Definition) {
			d.Steps = []automations.Step{
				{ID: "choose", Kind: automations.StepKindChoose, Choose: &automations.ChooseStep{}},
			}
		},
		"missing if payload": func(d *automations.Definition) {
			d.Steps = []automations.Step{{ID: "if", Kind: automations.StepKindIf}}
		},
		"duplicate branch IDs": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{
				{
					ID:   "choose",
					Kind: automations.StepKindChoose,
					Choose: &automations.ChooseStep{Branches: []automations.ChooseBranch{
						{ID: "same", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "first", 1)},
						{ID: "same", Conditions: triggerPredicate("a"), Steps: commandSequence(c, "second", 1)},
					}},
				},
			}
		},
		"foreign command payload": func(d *automations.Definition) { d.Steps[0].If = &automations.IfStep{} },
		"foreign branch fields": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", []automations.Step{c})}
			d.Steps[0].Parameters = devices.CommandParameters(`{}`)
		},
		"global duplicate": func(d *automations.Definition) {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", []automations.Step{c})}
			d.Steps[0].If.Else = []automations.Step{c}
		},
		"empty optional": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Else = []automations.Step{}
		},
		"missing trigger": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = triggerPredicate("missing")
		},
		"duplicate trigger refs": func(d *automations.Definition) {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = triggerPredicate("a", "a")
		},
		"trigger admission": func(d *automations.Definition) {
			root := automations.Condition{ID: "not", Kind: automations.ConditionNot}
			child := triggerPredicate("a")
			root.Child = &child
			d.Conditions = &root
		},
		"step cycle": func(d *automations.Definition) {
			s := ifNode("cycle", nil)
			s.If.Then = []automations.Step{s}
			d.Steps = []automations.Step{s}
		},
		"condition cycle": func(d *automations.Definition) {
			root := automations.Condition{ID: "cycle", Kind: automations.ConditionNot}
			root.Child = &root
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			d.Steps[0].If.Conditions = root
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

func TestBranchingStrictJSONVariants(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	d.Steps = []automations.Step{ifNode("if", d.Steps)}
	raw := string(wireDefinition(t, d))
	for name, invalid := range map[string]string{
		"command kind alias":    strings.Replace(raw, `"operation":"set"`, `"kind":"command","operation":"set"`, 1),
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
				ID:   automations.StepID(fmt.Sprintf("choose%d", i)),
				Kind: automations.StepKindChoose,
				Choose: &automations.ChooseStep{Branches: []automations.ChooseBranch{
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
	c.EntityID = newEntityID(t)
	stateEntity := newEntityID(t)
	nested := ifNode("unreachable-if", []automations.Step{c})
	nested.If.Conditions = automations.Condition{
		ID:   "state",
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: stateEntity,
			Operator: automations.ComparisonEqual,
			Operand:  json.RawMessage(`true`),
		},
	}
	d.Steps = []automations.Step{ifNode("outer", d.Steps)}
	d.Steps[0].If.Else = []automations.Step{nested}
	for _, arm := range []string{"if else", "choose alternative", "choose default"} {
		t.Run(arm, func(t *testing.T) {
			t.Parallel()
			definition := d
			if arm != "if else" {
				choose := &automations.ChooseStep{Branches: []automations.ChooseBranch{
					{
						ID:         "selected",
						Conditions: triggerPredicate("a"),
						Steps:      commandSequence(d.Steps[0].If.Then[0], "selected", 1),
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
				definition.Steps = []automations.Step{{ID: "choose", Kind: automations.StepKindChoose, Choose: choose}}
			}
			assertSaveReferenceValidation(t, definition, c.EntityID, stateEntity)
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
