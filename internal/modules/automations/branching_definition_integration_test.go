//go:build integration

package automations_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // Boundary table includes independent fixtures for every aggregate bound.
func TestBranchingBoundsAtDomainAndCodecBoundaries(t *testing.T) {
	t.Parallel()
	wide := func(n int) automations.Condition {
		root := automations.Condition{ID: "root", Body: automations.AllCondition{Children: nil}}
		for i := range n - 1 {
			leaf := triggerPredicate("a")
			leaf.ID = automations.ConditionID(fmt.Sprintf("leaf%d", i))
			allBody := root.Body.(automations.AllCondition)
			allBody.Children = append(allBody.Children, leaf)
			root.Body = allBody
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
				{ID: "choose", Body: automations.ChooseStep{Branches: []automations.ChooseBranch{
					{
						ID:         automations.BranchID(strings.Repeat("b", n)),
						Conditions: triggerPredicate("a"),
						Steps:      d.Steps,
					},
				}}},
			}
			return d
		}},
		{"sequence", 32, func(d automations.Definition, n int) automations.Definition {
			d.Steps = commandSequence(delayNode("wait", 1), "wait", n)
			return d
		}},
		{"commands across arms", 32, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			d.Steps = []automations.Step{ifNode("if", commandSequence(c, "then", 16))}
			ifBody := d.Steps[0].Body.(automations.IfStep)
			ifBody.Else = commandSequence(c, "else", n-16)
			d.Steps[0].Body = ifBody
			return d
		}},
		{"step depth", 8, func(d automations.Definition, n int) automations.Definition {
			step := delayNode("wait", 1)
			for i := 1; i < n; i++ {
				step = ifNode(fmt.Sprintf("if%d", i), []automations.Step{step})
			}
			d.Steps = []automations.Step{step}
			return d
		}},
		{"all steps", 64, func(d automations.Definition, n int) automations.Definition {
			c := delayNode("wait", 1)
			d.Steps = nil
			for i := range 32 {
				d.Steps = append(d.Steps, ifNode(fmt.Sprintf("if%d", i), commandSequence(c, fmt.Sprintf("c%d_", i), 1)))
			}
			if n > 64 {
				ifBody2 := d.Steps[0].Body.(automations.IfStep)
				ifBody2.Then = []automations.Step{ifNode("extra", d.Steps[0].Body.(automations.IfStep).Then)}
				d.Steps[0].Body = ifBody2
			}
			return d
		}},
		{"choose alternatives", 32, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			choose := automations.ChooseStep{}
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
			d.Steps = []automations.Step{{ID: "choose", Body: choose}}
			return d
		}},
		{"condition root nodes", 64, func(d automations.Definition, n int) automations.Definition {
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody3 := d.Steps[0].Body.(automations.IfStep)
			ifBody3.Conditions = wide(n)
			d.Steps[0].Body = ifBody3
			return d
		}},
		{"condition depth", 8, func(d automations.Definition, n int) automations.Definition {
			root := triggerPredicate("a")
			for i := 1; i < n; i++ {
				child := root
				root = automations.Condition{
					ID:   automations.ConditionID(fmt.Sprintf("not%d", i)),
					Body: automations.NotCondition{Child: child},
				}
			}
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody4 := d.Steps[0].Body.(automations.IfStep)
			ifBody4.Conditions = root
			d.Steps[0].Body = ifBody4
			return d
		}},
		{"total conditions", 256, func(d automations.Definition, n int) automations.Definition {
			c := d.Steps[0]
			d.Steps = nil
			for i := range 4 {
				s := ifNode(fmt.Sprintf("if%d", i), commandSequence(c, fmt.Sprintf("c%d_", i), 1))
				ifBody5 := s.Body.(automations.IfStep)
				ifBody5.Conditions = wide(64)
				s.Body = ifBody5
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
						automations.Trigger{ID: ids[i], Body: automations.CronTrigger{Expression: "* * * * *"}},
					)
				}
			}
			d.Steps = []automations.Step{ifNode("if", d.Steps)}
			ifBody6 := d.Steps[0].Body.(automations.IfStep)
			ifBody6.Conditions = triggerPredicate(ids...)
			d.Steps[0].Body = ifBody6
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
	ifBody := d.Steps[0].Body.(automations.IfStep)
	commandBody := ifBody.Then[0].Body.(automations.CommandStep)
	commandBody.Parameters = devices.CommandParameters(`{"value":""}`)
	ifBody.Then[0].Body = commandBody
	d.Steps[0].Body = ifBody
	baseSize := len(wireDefinition(t, d))
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	for _, size := range []int{65536, 65537} {
		ifBody2 := d.Steps[0].Body.(automations.IfStep)
		commandBody2 := ifBody2.Then[0].Body.(automations.CommandStep)
		commandBody2.Parameters = devices.CommandParameters(
			`{"value":"` + strings.Repeat("x", size-baseSize) + `"}`,
		)
		ifBody2.Then[0].Body = commandBody2

		d.Steps[0].Body = ifBody2

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
