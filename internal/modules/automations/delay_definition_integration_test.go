//go:build integration

package automations_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

//nolint:gocognit // The typed-input matrix checks both Service write boundaries and their unchanged baseline.
func TestServiceRejectsMalformedTypedDelaysBeforePersistence(t *testing.T) {
	t.Parallel()
	mutations := map[string]func(*automations.Definition){
		"missing payload":   func(d *automations.Definition) { d.Steps[0].Body = nil },
		"pointer payload":   func(d *automations.Definition) { d.Steps[0].Body = &automations.DelayStep{DurationMS: 1} },
		"typed nil payload": func(d *automations.Definition) { d.Steps[0].Body = (*automations.DelayStep)(nil) },
		"zero":              func(d *automations.Definition) { d.Steps[0].Body = automations.DelayStep{} },
		"negative":          func(d *automations.Definition) { d.Steps[0].Body = automations.DelayStep{DurationMS: -1} },
		"above maximum":     func(d *automations.Definition) { d.Steps[0].Body = automations.DelayStep{DurationMS: 86400001} },
		"overflow":          func(d *automations.Definition) { d.Steps[0].Body = automations.DelayStep{DurationMS: math.MaxInt64} },
		"duplicate ID":      func(d *automations.Definition) { d.Steps = append(d.Steps, delayNode("wait", 2)) },
		"empty sequence":    func(d *automations.Definition) { d.Steps = nil },
		"too many children": func(d *automations.Definition) { d.Steps = commandSequence(delayNode("wait", 1), "wait", 33) },
		"too many nodes": func(d *automations.Definition) {
			d.Steps = nil
			for i := range 3 {
				d.Steps = append(
					d.Steps,
					ifNode(
						fmt.Sprintf("route%d", i),
						commandSequence(delayNode("wait", 1), fmt.Sprintf("wait%d_", i), 21),
					),
				)
			}
		},
		"too deep": func(d *automations.Definition) {
			step := delayNode("wait", 1)
			for i := range 8 {
				step = ifNode(fmt.Sprintf("route%d", i), []automations.Step{step})
			}
			d.Steps = []automations.Step{step}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repository := &delayDefinitionWrites{
				Repository: automationssqlite.NewAutomationRepository(
					openAutomationDatabase(t),
					runtimeTestDependencies(),
				),
			}
			service := automations.NewService(repository, newScriptedDevices(), runtimeTestDependencies())
			definition := branchingFixture(t)
			definition.Steps = []automations.Step{delayNode("wait", 1)}
			original, err := service.CreateAutomation(ctx, definition)
			if err != nil {
				t.Fatal(err)
			}
			repository.creates = 0
			mutate(&definition)
			if _, err = service.CreateAutomation(ctx, definition); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("create = %v", err)
			}
			if _, err = service.ReplaceAutomation(
				ctx,
				original.ID,
				original.Revision,
				definition,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("replace = %v", err)
			}
			if repository.creates != 0 || repository.replacements != 0 {
				t.Fatalf(
					"malformed input reached persistence: create=%d replace=%d",
					repository.creates,
					repository.replacements,
				)
			}
			got, err := service.GetAutomation(ctx, original.ID)
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Fatalf("invalid write changed baseline: %#v, %v", got, err)
			}
		})
	}
}
