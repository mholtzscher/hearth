package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

func delayNode(id string, duration int64) automations.Step {
	return automations.Step{ID: automations.StepID(id), Body: automations.DelayStep{DurationMS: duration}}
}

// Count calls before SQLite validation so repository rejection cannot hide a
// missing Service guard.
type delayDefinitionWrites struct {
	automations.Repository

	creates, replacements int
}

func (repository *delayDefinitionWrites) CreateAutomation(
	ctx context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	repository.creates++
	return repository.Repository.CreateAutomation(ctx, definition)
}

func (repository *delayDefinitionWrites) ReplaceAutomation(
	ctx context.Context,
	id automations.AutomationID,
	revision int64,
	definition automations.Definition,
) (automations.Record, error) {
	repository.replacements++
	return repository.Repository.ReplaceAutomation(ctx, id, revision, definition)
}

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

func delayDocument(steps string) json.RawMessage {
	return json.RawMessage(
		`{"name":"Wait","enabled":true,"triggers":[{"id":"a","kind":"cron","expression":"* * * * *"}],"steps":[` + steps + `]}`,
	)
}

// Duration boundaries and literal canonical bytes come from the delay contract,
// not from the encoder. Mutation of caller memory must not change prepared input.
func TestDelayDefinitionCanonicalDurationAndOwnedPayload(t *testing.T) {
	t.Parallel()
	for _, duration := range []int64{1, 1001, 300000, 86400000} {
		t.Run(strconv.FormatInt(duration, 10), func(t *testing.T) {
			t.Parallel()
			want := delayDocument(fmt.Sprintf(`{"id":"wait","kind":"delay","duration_ms":%d}`, duration))
			decoded, err := automations.DecodeDefinition(want)
			if err != nil {
				t.Fatal(err)
			}
			if got := decoded.Steps[0]; got.Kind() != automations.StepKindDelay ||
				got.Body.(automations.DelayStep).DurationMS != duration {
				t.Fatalf("decoded delay = %#v", got)
			}
			normalized, raw, err := automations.NormalizeAndEncodeDefinition(decoded)
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("canonical encoding = %s, %v; want %s", raw, err, want)
			}
			decoded.Steps[0].Body = automations.DelayStep{}
			raw, err = automations.EncodeDefinition(normalized)
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("normalized delay aliases caller: %s, %v", raw, err)
			}
		})
	}
}

func TestDelayDefinitionRejectsInvalidWireFamiliesAndDurations(t *testing.T) {
	t.Parallel()
	steps := []string{
		`{"id":"wait","kind":"delay"}`,
		`{"id":"wait","duration_ms":1}`,
		`{"id":"wait","kind":"unknown","duration_ms":1}`,
		`{"id":"Bad ID","kind":"delay","duration_ms":1}`,
		`{"id":"wait","kind":"delay","duration_ms":1},{"id":"wait","kind":"delay","duration_ms":2}`,
	}
	for _, duration := range []string{"null", "0", "-1", "0.5", "1.5", "86400001", "9223372036854775808", "1e1000", `"1"`, "true"} {
		steps = append(steps, fmt.Sprintf(`{"id":"wait","kind":"delay","duration_ms":%s}`, duration))
	}
	for _, field := range []string{"entity_id", "operation", "parameters", "conditions", "then", "else", "branches", "default", "extra"} {
		steps = append(steps, fmt.Sprintf(`{"id":"wait","kind":"delay","duration_ms":1,%q:null}`, field))
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			if _, err := automations.DecodeDefinition(
				delayDocument(step),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("decode = %v, want invalid automation", err)
			}
		})
	}
}

func TestDelayDefinitionRejectsInvalidValueBodies(t *testing.T) {
	t.Parallel()
	tests := map[string]automations.StepBody{
		"missing payload":   nil,
		"pointer payload":   &automations.DelayStep{DurationMS: 1},
		"typed nil payload": (*automations.DelayStep)(nil),
		"zero":              automations.DelayStep{},
		"negative":          automations.DelayStep{DurationMS: -1},
		"above maximum":     automations.DelayStep{DurationMS: 86400001},
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := branchingFixture(t)
			d.Steps = []automations.Step{{ID: "wait", Body: body}}
			if _, err := automations.EncodeDefinition(d); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("encode = %v, want invalid automation", err)
			}
		})
	}
}

// Nested waits survive encoding in every arm but create neither command positions
// nor device references during definition validation.
func TestNestedDelaysPreserveCommandPositionsAndNeedNoDeviceReferences(t *testing.T) {
	t.Parallel()
	d := branchingFixture(t)
	c := d.Steps[0]
	d.Steps = []automations.Step{
		delayNode("first", 1),
		ifNode("if", []automations.Step{delayNode("then-wait", 2), c}),
		{ID: "choose", Body: automations.ChooseStep{
			Branches: []automations.ChooseBranch{
				{ID: "arm", Conditions: triggerPredicate("a"), Steps: []automations.Step{delayNode("arm-wait", 3)}},
			},
			Default: []automations.Step{delayNode("default-wait", 4)},
		}},
	}
	branch := d.Steps[1].Body.(automations.IfStep)
	branch.Else = []automations.Step{delayNode("else-wait", 5)}
	d.Steps[1].Body = branch
	normalized, raw, err := automations.NormalizeAndEncodeDefinition(d)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(raw)
	if err != nil || !reflect.DeepEqual(decoded.Steps, normalized.Steps) {
		t.Fatalf("nested delay round trip = %#v, %v", decoded.Steps, err)
	}
	leaves := automations.CommandLeaves(decoded.Steps)
	if len(leaves) != 1 || leaves[0].ID != c.ID {
		t.Fatalf("command leaves = %#v", leaves)
	}
	// No Devices method can be called for cron, Trigger predicates, or delays.
	branch.Then = []automations.Step{delayNode("then-wait", 2)}
	d.Steps[1].Body = branch
	stub := &branchingReferenceDevices{commandError: errors.New("delay must not validate a Command")}
	if _, err = automations.ValidateDefinition(context.Background(), stub, d); err != nil {
		t.Fatalf("delay-only references rejected: %v", err)
	}
	if stub.stateReads != 0 {
		t.Fatalf("delay-only definition read State %d times", stub.stateReads)
	}
}
