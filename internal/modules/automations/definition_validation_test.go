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

// An embedded sealed interface does not make a foreign implementation supported.
type unsupportedTriggerBody struct{ automations.TriggerBody }

func TestNormalizeAutomationDefinitionRejectsUnsupportedTriggerBodies(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]automations.TriggerBody{
		"nil":                 nil,
		"observation pointer": typedObservationTrigger(t),
		"event pointer":       typedEntityEventTrigger(t),
		"held pointer":        &automations.HeldStateTrigger{},
		"cron pointer":        &automations.CronTrigger{Expression: "* * * * *"},
		"typed nil":           (*automations.CronTrigger)(nil),
		"embedded interface":  unsupportedTriggerBody{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			definition := validDomainDefinition(t)
			definition.Triggers[0].Body = body
			if _, err := automations.NormalizeDefinition(
				definition,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("normalization = %v", err)
			}
		})
	}
}

func TestNormalizeCronDefinitionOwnsPayload(t *testing.T) {
	t.Parallel()
	definition := cronDefinition(t, " \t0  7 * * MON-FRI ")
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	cronBody := definition.Triggers[0].Body.(automations.CronTrigger)
	cronBody.Expression = "* * * * *"
	definition.Triggers[0].Body = cronBody
	if normalized.Triggers[0].Body.(automations.CronTrigger).Expression != "0 7 * * MON-FRI" {
		t.Fatalf("normalized expression = %q", normalized.Triggers[0].Body.(automations.CronTrigger).Expression)
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
		{
			"unknown trigger kind",
			func(definition *automations.Definition) { definition.Triggers[0].Body = unsupportedTriggerBody{} },
		},
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
					observationBody := item.Body.(automations.ObservationTrigger)
					observationBody.Dispositions = nil
					item.Body = observationBody
				})
			},
		},
		{
			"duplicate dispositions",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody2 := item.Body.(automations.ObservationTrigger)
					observationBody2.Dispositions = []devices.ObservationDisposition{
						devices.DispositionApplied, devices.DispositionApplied,
					}
					item.Body = observationBody2
				})
			},
		},
		{
			"unknown disposition",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody3 := item.Body.(automations.ObservationTrigger)
					observationBody3.Dispositions = []devices.ObservationDisposition{
						devices.ObservationDisposition("rejected"),
					}
					item.Body = observationBody3
				})
			},
		},
		{
			"too many comparisons",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody4 := item.Body.(automations.ObservationTrigger)
					observationBody4.Comparisons = nil
					item.Body = observationBody4
					for index := range tooManyComparisonsN {
						observationBody5 := item.Body.(automations.ObservationTrigger)
						observationBody5.Comparisons = append(observationBody5.Comparisons, comparison(
							fmt.Sprintf("/c%d", index), automations.ComparisonEqual, "1",
						))
						item.Body = observationBody5
					}
				})
			},
		},
		{
			"bad pointer",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody6 := item.Body.(automations.ObservationTrigger)
					observationBody6.Comparisons = []automations.ObservationComparison{
						comparison("temperature", automations.ComparisonEqual, "1"),
					}
					item.Body = observationBody6
				})
			},
		},
		{
			"unknown operator",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody7 := item.Body.(automations.ObservationTrigger)
					observationBody7.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonOperator("between"), "1"),
					}
					item.Body = observationBody7
				})
			},
		},
		{
			"ordering operand not numeric",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody8 := item.Body.(automations.ObservationTrigger)
					observationBody8.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonGreaterThan, `"twenty"`),
					}
					item.Body = observationBody8
				})
			},
		},
		{
			"bad observation entity id",
			func(definition *automations.Definition) {
				trigger(definition, func(item *automations.Trigger) {
					observationBody9 := item.Body.(automations.ObservationTrigger)
					observationBody9.EntityID = "ent_not-a-uuid"
					item.Body = observationBody9
				})
			},
		},
		{
			"bad event name",
			func(definition *automations.Definition) {
				definition.Triggers[0] = automations.Trigger{ID: "press", Body: automations.EntityEventTrigger{
					EntityID: newEntityID(t), EventName: "Bad Name",
				}}
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
			func(definition *automations.Definition) {
				commandBody := definition.Steps[0].Body.(automations.CommandStep)
				commandBody.OperationName = "Set It"
				definition.Steps[0].Body = commandBody
			},
		},
		{
			"bad step entity id",
			func(definition *automations.Definition) {
				commandBody2 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody2.EntityID = "ent_not-a-uuid"
				definition.Steps[0].Body = commandBody2
			},
		},
		{
			"parameters not an object",
			func(definition *automations.Definition) {
				commandBody3 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody3.Parameters = devices.CommandParameters(`true`)
				definition.Steps[0].Body = commandBody3
			},
		},
		{
			"parameters are null",
			func(definition *automations.Definition) {
				commandBody4 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody4.Parameters = devices.CommandParameters(`null`)
				definition.Steps[0].Body = commandBody4
			},
		},
		{
			"parameters are invalid json",
			func(definition *automations.Definition) {
				commandBody5 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody5.Parameters = devices.CommandParameters(`{"value":`)
				definition.Steps[0].Body = commandBody5
			},
		},
		{
			"parameters are empty",
			func(definition *automations.Definition) {
				commandBody6 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody6.Parameters = nil
				definition.Steps[0].Body = commandBody6
			},
		},
		{
			"parameters carry trailing values",
			func(definition *automations.Definition) {
				commandBody7 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody7.Parameters = devices.CommandParameters(`{"value":true} false`)
				definition.Steps[0].Body = commandBody7
			},
		},
		{
			"encoded definition too large",
			func(definition *automations.Definition) {
				commandBody8 := definition.Steps[0].Body.(automations.CommandStep)
				commandBody8.Parameters = devices.CommandParameters(
					fmt.Sprintf(`{"value":%q}`, tooLongParameter()),
				)
				definition.Steps[0].Body = commandBody8
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
		Triggers: []automations.Trigger{{ID: "occupied", Body: automations.ObservationTrigger{
			EntityID:     newEntityID(t),
			Dispositions: dispositions,
			Comparisons: []automations.ObservationComparison{{
				Pointer:  "/occupied",
				Operator: automations.ComparisonEqual,
				Operand:  operand,
			}},
		}}},
		Steps: []automations.Step{
			{
				ID: "light_on",
				Body: automations.CommandStep{
					EntityID:      newEntityID(t),
					OperationName: devices.OperationNameSet,
					Parameters:    parameters,
				},
			},
		},
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
	definition.Triggers[0].Body = nil
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
		Triggers: []automations.Trigger{{ID: "held", Body: automations.HeldStateTrigger{
			EntityID: triggerEntity, ForSeconds: 60,
			Comparisons: []automations.ObservationComparison{{
				Pointer: "/on", Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
			}},
		}}},
		Steps: []automations.Step{
			{
				ID: "step",
				Body: automations.CommandStep{
					EntityID:      stepEntity,
					OperationName: devices.OperationNameSet,
					Parameters:    devices.CommandParameters(`{"value":true}`),
				},
			},
		},
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
