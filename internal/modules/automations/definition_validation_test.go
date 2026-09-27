package automations_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestNormalizeAutomationDefinitionRejectsContradictoryTriggerFamilies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		kind   automations.TriggerKind
		mutate func(trigger *automations.Trigger)
	}{
		{
			"observation carries event payload",
			automations.TriggerKindObservation,
			func(trigger *automations.Trigger) {
				trigger.EntityEvent = typedEntityEventTrigger(t)
			},
		},
		{
			"observation without observation payload",
			automations.TriggerKindObservation,
			func(trigger *automations.Trigger) { trigger.Observation = nil },
		},
		{
			"entity event carries observation payload",
			automations.TriggerKindEntityEvent,
			func(trigger *automations.Trigger) {
				trigger.EntityEvent = typedEntityEventTrigger(t)
				trigger.Observation = typedObservationTrigger(t)
			},
		},
		{
			"entity event without event payload",
			automations.TriggerKindEntityEvent,
			func(trigger *automations.Trigger) {
				trigger.Observation = nil
				trigger.EntityEvent = nil
			},
		},
		{
			"unknown kind carries a payload",
			automations.TriggerKind("cron"),
			func(trigger *automations.Trigger) {
				trigger.Observation = typedObservationTrigger(t)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := validDomainDefinition(t)
			definition.Triggers[0].Kind = test.kind
			test.mutate(&definition.Triggers[0])
			_, err := automations.NormalizeDefinition(definition)
			if err == nil {
				t.Fatal("contradictory typed trigger was normalized")
			}
			if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("error = %v, want ErrInvalidAutomation", err)
			}
		})
	}
}

// Typed normalization must enforce the same structural and size limits as
// strict JSON decoding.
func TestNormalizeAutomationDefinitionRejectsMalformedTypedDefinitions(t *testing.T) {
	t.Parallel()
	trigger := func(definition *automations.Definition, mutate func(*automations.Trigger)) {
		mutate(&definition.Triggers[0])
	}
	tests := []struct {
		name   string
		mutate func(definition *automations.Definition)
	}{
		{"empty name", func(definition *automations.Definition) { definition.Name = "" }},
		{"whitespace name", func(definition *automations.Definition) { definition.Name = "   " }},
		{
			"untrimmed name beyond bound",
			func(definition *automations.Definition) {
				definition.Name = " " + strings.Repeat("x", 200)
			},
		},
		{"no triggers", func(definition *automations.Definition) { definition.Triggers = nil }},
		{
			"too many triggers",
			func(definition *automations.Definition) {
				definition.Triggers = slices.Repeat(definition.Triggers[:1], tooManyTriggersN)
				for index := range definition.Triggers {
					definition.Triggers[index].ID = automations.TriggerID(fmt.Sprintf("trigger_%d", index))
				}
			},
		},
		{
			"duplicate trigger ids",
			func(definition *automations.Definition) {
				definition.Triggers = append(definition.Triggers, definition.Triggers[0])
			},
		},
		{
			"bad trigger slug",
			func(definition *automations.Definition) { definition.Triggers[0].ID = "Bad ID" },
		},
		{
			"no dispositions",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Dispositions = nil
				})
			},
		},
		{
			"duplicate dispositions",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Dispositions = []devices.ObservationDisposition{
						devices.DispositionApplied, devices.DispositionApplied,
					}
				})
			},
		},
		{
			"unknown disposition",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Dispositions = []devices.ObservationDisposition{
						devices.ObservationDisposition("rejected"),
					}
				})
			},
		},
		{
			"too many comparisons",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Comparisons = nil
					for index := range tooManyComparisonsN {
						item.Observation.Comparisons = append(item.Observation.Comparisons, comparison(
							fmt.Sprintf("/c%d", index), automations.ComparisonEqual, "1",
						))
					}
				})
			},
		},
		{
			"bad pointer",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("temperature", automations.ComparisonEqual, "1"),
					}
				})
			},
		},
		{
			"unknown operator",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonOperator("between"), "1"),
					}
				})
			},
		},
		{
			"ordering operand not numeric",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonGreaterThan, `"twenty"`),
					}
				})
			},
		},
		{
			"bad observation entity id",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					item.Observation.EntityID = "ent_not-a-uuid"
				})
			},
		},
		{
			"bad event name",
			func(definition *automations.Definition) {
				definition.Triggers[0] = automations.Trigger{
					ID: "press", Kind: automations.TriggerKindEntityEvent,
					EntityEvent: &automations.EntityEventTrigger{
						EntityID: newEntityID(t), EventName: "Bad Name",
					},
				}
			},
		},
		{"no steps", func(definition *automations.Definition) { definition.Steps = nil }},
		{
			"too many steps",
			func(definition *automations.Definition) {
				definition.Steps = slices.Repeat(definition.Steps[:1], tooManyStepsN)
				for index := range definition.Steps {
					definition.Steps[index].ID = automations.StepID(fmt.Sprintf("step_%d", index))
				}
			},
		},
		{
			"duplicate step ids",
			func(definition *automations.Definition) {
				definition.Steps = append(definition.Steps, definition.Steps[0])
			},
		},
		{"bad step slug", func(definition *automations.Definition) { definition.Steps[0].ID = "S" }},
		{
			"bad operation slug",
			func(definition *automations.Definition) { definition.Steps[0].OperationName = "Set It" },
		},
		{
			"bad step entity id",
			func(definition *automations.Definition) { definition.Steps[0].EntityID = "ent_not-a-uuid" },
		},
		{
			"parameters not an object",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`true`)
			},
		},
		{
			"parameters are null",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`null`)
			},
		},
		{
			"parameters are invalid json",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`{"value":`)
			},
		},
		{
			"parameters are empty",
			func(definition *automations.Definition) { definition.Steps[0].Parameters = nil },
		},
		{
			"parameters carry trailing values",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`{"value":true} false`)
			},
		},
		{
			"encoded definition too large",
			func(definition *automations.Definition) {
				definition.Steps[0].Parameters = devices.CommandParameters(
					fmt.Sprintf(`{"value":%q}`, tooLongParameter()),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := validDomainDefinition(t)
			test.mutate(&definition)
			_, err := automations.NormalizeDefinition(definition)
			if err == nil {
				t.Fatal("malformed typed definition was normalized")
			}
			if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("error = %v, want ErrInvalidAutomation", err)
			}
		})
	}
}

// Typed and JSON normalization must produce the same canonical representation.
func TestNormalizeAutomationDefinitionMatchesDecodedForm(t *testing.T) {
	t.Parallel()
	raw := definitionFixture(t, newEntityID(t), newEntityID(t), newEntityID(t))
	decoded, err := automations.DecodeDefinition(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := automations.NormalizeDefinition(decoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := automations.EncodeDefinition(decoded)
	if err != nil {
		t.Fatal(err)
	}
	normalizedRaw, err := automations.EncodeDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if string(decodedRaw) != string(normalizedRaw) {
		t.Fatalf("direct normalization diverged:\n%s\n%s", decodedRaw, normalizedRaw)
	}
}

// Caller mutations must not change the normalized definition or its encoded bytes.
func TestNormalizeAutomationDefinitionOwnsCallerMemory(t *testing.T) {
	t.Parallel()
	dispositions := []devices.ObservationDisposition{devices.DispositionUnchanged, devices.DispositionApplied}
	operand := json.RawMessage(`{"occupied":true}`)
	parameters := devices.CommandParameters(`{"value":true}`)
	definition := automations.Definition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.Trigger{{
			ID:   "occupied",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: dispositions,
				Comparisons: []automations.ObservationComparison{{
					Pointer:  "/occupied",
					Operator: automations.ComparisonEqual,
					Operand:  operand,
				}},
			},
		}},
		Steps: []automations.Step{{
			ID:            "light_on",
			EntityID:      newEntityID(t),
			OperationName: devices.OperationNameSet,
			Parameters:    parameters,
		}},
	}
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	before, err := automations.EncodeDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	dispositions[0] = devices.DispositionApplied
	operand[1] = 'x'
	parameters[0] = ' '
	definition.Triggers[0].Observation = nil
	after, err := automations.EncodeDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("normalized definition aliases caller memory:\n%s\n%s", before, after)
	}
}
