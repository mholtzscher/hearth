package automations_test

import (
	"context"
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
	entityID := newEntityID(t)
	payloads := []automations.Trigger{
		{ID: "t", Kind: automations.TriggerKindObservation, Observation: typedObservationTrigger(t)},
		{ID: "t", Kind: automations.TriggerKindEntityEvent, EntityEvent: typedEntityEventTrigger(t)},
		{ID: "t", Kind: automations.TriggerKindHeldState, HeldState: &automations.HeldStateTrigger{
			EntityID: entityID, ForSeconds: 60,
			Comparisons: []automations.ObservationComparison{comparison("", automations.ComparisonEqual, "true")},
		}},
		{ID: "t", Kind: automations.TriggerKindCron, Cron: &automations.CronTrigger{Expression: "0 7 * * *"}},
	}
	for _, family := range payloads {
		for _, extra := range payloads {
			if family.Kind == extra.Kind {
				continue
			}
			t.Run(string(family.Kind)+" with "+string(extra.Kind), func(t *testing.T) {
				t.Parallel()
				definition := validDomainDefinition(t)
				trigger := family
				addTypedTriggerPayload(&trigger, extra)
				definition.Triggers = []automations.Trigger{trigger}
				if _, err := automations.NormalizeDefinition(
					definition,
				); !errors.Is(
					err,
					automations.ErrInvalidAutomation,
				) {
					t.Fatalf("error = %v, want invalid automation", err)
				}
			})
		}
		definition := validDomainDefinition(t)
		definition.Triggers = []automations.Trigger{{ID: "t", Kind: family.Kind}}
		if _, err := automations.NormalizeDefinition(definition); !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Fatalf("%s missing payload: %v", family.Kind, err)
		}
	}
}

func addTypedTriggerPayload(trigger *automations.Trigger, extra automations.Trigger) {
	switch extra.Kind {
	case automations.TriggerKindObservation:
		trigger.Observation = extra.Observation
	case automations.TriggerKindEntityEvent:
		trigger.EntityEvent = extra.EntityEvent
	case automations.TriggerKindHeldState:
		trigger.HeldState = extra.HeldState
	case automations.TriggerKindCron:
		trigger.Cron = extra.Cron
	}
}

func TestNormalizeCronDefinitionOwnsPayload(t *testing.T) {
	t.Parallel()
	definition := cronDefinition(t, " \t0  7 * * MON-FRI ")
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Triggers[0].Cron.Expression = "* * * * *"
	if normalized.Triggers[0].Cron.Expression != "0 7 * * MON-FRI" {
		t.Fatalf("normalized expression = %q", normalized.Triggers[0].Cron.Expression)
	}
	if normalized.Triggers[0].EntityID() != "" {
		t.Fatal("cron has an Entity reference")
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
		{"unknown trigger kind", func(definition *automations.Definition) { definition.Triggers[0].Kind = "unknown" }},
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

func TestValidateAutomationDefinitionValidatesHeldStateEntityAndPointers(t *testing.T) {
	t.Parallel()
	stub := &stubAutomationDevices{}
	triggerEntity := newEntityID(t)
	stepEntity := newEntityID(t)
	definition := automations.Definition{
		Name:    "Held",
		Enabled: true,
		Triggers: []automations.Trigger{{
			ID: "held", Kind: automations.TriggerKindHeldState,
			HeldState: &automations.HeldStateTrigger{
				EntityID: triggerEntity, ForSeconds: 60,
				Comparisons: []automations.ObservationComparison{{
					Pointer: "/on", Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
				}},
			},
		}},
		Steps: []automations.Step{{
			ID: "step", EntityID: stepEntity, OperationName: devices.OperationNameSet,
			Parameters: devices.CommandParameters(`{"value":true}`),
		}},
	}
	if _, err := automations.ValidateDefinition(context.Background(), stub, definition); err != nil {
		t.Fatalf("stateful held trigger should validate: %v", err)
	}
	if len(stub.observationCalls) != 1 || stub.observationCalls[0] != triggerEntity {
		t.Fatalf("held trigger reference calls = %v, want entity pointer validation", stub.observationCalls)
	}
	stub.observationError = errors.New("stateless entity")
	if _, err := automations.ValidateDefinition(
		context.Background(), stub, definition,
	); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("stateless held trigger error = %v, want invalid automation", err)
	}
}
