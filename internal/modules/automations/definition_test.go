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

// newEntityID mints one canonical Entity identity for a fixture.
func newEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	id, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// definitionFixture builds one valid strict definition document. Lengths are
// caller-controlled so bounds can be probed without duplicating the shape.
func definitionFixture(
	t *testing.T,
	observationEntity devices.EntityID,
	eventEntity devices.EntityID,
	actionEntity devices.EntityID,
) string {
	t.Helper()
	return fmt.Sprintf(`{
  "name": "  Button warms the office  ",
  "enabled": true,
  "triggers": [
    {
      "id": "occupied_and_warm",
      "kind": "observation",
      "entity_id": %q,
      "dispositions": ["unchanged", "applied"],
      "comparisons": [
        {"pointer": "/temperature", "operator": "gt", "operand": 20},
        {"pointer": "", "operator": "eq", "operand": {"occupied": true}}
      ]
    },
    {
      "id": "single_press",
      "kind": "entity_event",
      "entity_id": %q,
      "event_name": "single_press"
    }
  ],
  "steps": [
    {
      "id": "light_on",
      "entity_id": %q,
      "operation": "set",
      "parameters": {"value": true}
    }
  ]
}`, observationEntity, eventEntity, actionEntity)
}

func decodeDefinition(t *testing.T, raw string) (automations.AutomationDefinition, error) {
	t.Helper()
	return automations.DecodeAutomationDefinition(json.RawMessage(raw))
}

// Normalization trims names and orders dispositions without changing Trigger
// identities, family payloads, or Step order.
func TestDecodeAutomationDefinitionNormalizesValidDocuments(t *testing.T) {
	t.Parallel()
	observationEntity := newEntityID(t)
	eventEntity := newEntityID(t)
	actionEntity := newEntityID(t)

	definition, err := decodeDefinition(t, definitionFixture(t, observationEntity, eventEntity, actionEntity))
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "Button warms the office" {
		t.Fatalf("name = %q, want trimmed name", definition.Name)
	}
	if !definition.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if len(definition.Triggers) != 2 || len(definition.Steps) != 1 {
		t.Fatalf("definition = %#v", definition)
	}
	observation := definition.Triggers[0]
	if observation.Kind != automations.TriggerKindObservation || observation.Observation == nil ||
		observation.EntityEvent != nil {
		t.Fatalf("observation trigger = %#v", observation)
	}
	wantDispositions := []devices.ObservationDisposition{devices.DispositionApplied, devices.DispositionUnchanged}
	if len(observation.Observation.Dispositions) != 2 ||
		observation.Observation.Dispositions[0] != wantDispositions[0] ||
		observation.Observation.Dispositions[1] != wantDispositions[1] {
		t.Fatalf("dispositions = %#v, want canonical order", observation.Observation.Dispositions)
	}
	if len(observation.Observation.Comparisons) != 2 {
		t.Fatalf("comparisons = %#v", observation.Observation.Comparisons)
	}
	entityEvent := definition.Triggers[1]
	if entityEvent.Kind != automations.TriggerKindEntityEvent || entityEvent.EntityEvent == nil ||
		entityEvent.Observation != nil || entityEvent.EntityEvent.EventName != "single_press" {
		t.Fatalf("entity event trigger = %#v", entityEvent)
	}
	step := definition.Steps[0]
	if step.ID != "light_on" || step.EntityID != actionEntity || step.OperationName != devices.OperationNameSet {
		t.Fatalf("step = %#v", step)
	}
}

// A second decode/encode pass must preserve the canonical bytes.
func TestDecodeAutomationDefinitionIsStableUnderRoundTrip(t *testing.T) {
	t.Parallel()
	first, err := decodeDefinition(t, definitionFixture(t, newEntityID(t), newEntityID(t), newEntityID(t)))
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, err := automations.EncodeAutomationDefinition(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := automations.DecodeAutomationDefinition(firstRaw)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := automations.EncodeAutomationDefinition(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", firstRaw, secondRaw)
	}
}

// Malformed definitions must fail as permanent input errors before persistence.
func TestDecodeAutomationDefinitionRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	observationEntity := newEntityID(t)
	eventEntity := newEntityID(t)
	actionEntity := newEntityID(t)
	valid := definitionFixture(t, observationEntity, eventEntity, actionEntity)

	triggers := func(body string) string {
		return fmt.Sprintf(
			`{"name":"n","triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
			body, actionEntity,
		)
	}
	observationTrigger := func(body string) string {
		return fmt.Sprintf(
			`{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"],%s}`,
			observationEntity,
			body,
		)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{"unknown root field", `{"name":"n","enabled":true,"triggers":[],"steps":[],"extra":1}`},
		{
			"unknown trigger field",
			triggers(
				fmt.Sprintf(
					`{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"],"extra":1}`,
					observationEntity,
				),
			),
		},
		{
			"unknown step field",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{},"extra":1}]}`,
				observationTrigger(`"comparisons":[]`),
				actionEntity,
			),
		},
		{
			"unknown comparison field",
			triggers(observationTrigger(`"comparisons":[{"pointer":"/a","operator":"eq","operand":1,"extra":1}]`)),
		},
		{
			"unknown kind",
			triggers(
				fmt.Sprintf(`{"id":"t","kind":"cron","entity_id":%q,"dispositions":["applied"]}`, observationEntity),
			),
		},
		{
			"observation carries event name",
			triggers(
				fmt.Sprintf(
					`{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"],"event_name":"x"}`,
					observationEntity,
				),
			),
		},
		{
			"entity event carries dispositions",
			triggers(
				fmt.Sprintf(
					`{"id":"t","kind":"entity_event","entity_id":%q,"event_name":"x","dispositions":["applied"]}`,
					eventEntity,
				),
			),
		},
		{
			"no triggers",
			`{"name":"n","triggers":[],"steps":[{"id":"s","entity_id":"` + string(
				actionEntity,
			) + `","operation":"set","parameters":{}}]}`,
		},
		{"too many triggers", triggers(tooManyTriggers(t, observationEntity))},
		{
			"duplicate trigger ids",
			triggers(observationTrigger(`"comparisons":[]`) + "," + observationTrigger(`"comparisons":[]`)),
		},
		{
			"bad trigger slug",
			triggers(
				fmt.Sprintf(
					`{"id":"Bad ID","kind":"observation","entity_id":%q,"dispositions":["applied"]}`,
					observationEntity,
				),
			),
		},
		{
			"no dispositions",
			triggers(
				fmt.Sprintf(`{"id":"t","kind":"observation","entity_id":%q,"dispositions":[]}`, observationEntity),
			),
		},
		{
			"duplicate dispositions",
			triggers(
				fmt.Sprintf(
					`{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied","applied"]}`,
					observationEntity,
				),
			),
		},
		{
			"unknown disposition",
			triggers(
				fmt.Sprintf(
					`{"id":"t","kind":"observation","entity_id":%q,"dispositions":["rejected"]}`,
					observationEntity,
				),
			),
		},
		{"too many comparisons", triggers(observationTrigger("\"comparisons\":" + tooManyComparisons()))},
		{
			"pointer without slash",
			triggers(observationTrigger(`"comparisons":[{"pointer":"temperature","operator":"eq","operand":1}]`)),
		},
		{
			"pointer bad escape",
			triggers(observationTrigger(`"comparisons":[{"pointer":"/bad~2escape","operator":"eq","operand":1}]`)),
		},
		{
			"pointer trailing escape",
			triggers(observationTrigger(`"comparisons":[{"pointer":"/trailing~","operator":"eq","operand":1}]`)),
		},
		{"pointer too long", triggers(observationTrigger(tooLongPointer()))},
		{
			"ordering operand not numeric",
			triggers(observationTrigger(`"comparisons":[{"pointer":"/a","operator":"gt","operand":"twenty"}]`)),
		},
		{
			"unknown operator",
			triggers(observationTrigger(`"comparisons":[{"pointer":"/a","operator":"between","operand":1}]`)),
		},
		{"missing operand", triggers(observationTrigger(`"comparisons":[{"pointer":"/a","operator":"eq"}]`))},
		{
			"bad entity id",
			triggers(
				`{"id":"t","kind":"observation","entity_id":"ent_not-a-uuid","dispositions":["applied"]}`,
			),
		},
		{"no steps", fmt.Sprintf(`{"name":"n","triggers":[%s],"steps":[]}`, observationTrigger(`"comparisons":[]`))},
		{
			"too many steps",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[%s]}`,
				observationTrigger(`"comparisons":[]`),
				tooManySteps(t, actionEntity),
			),
		},
		{
			"duplicate step ids",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}},{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
				observationTrigger(`"comparisons":[]`),
				actionEntity,
				actionEntity,
			),
		},
		{
			"bad step slug",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[{"id":"S","entity_id":%q,"operation":"set","parameters":{}}]}`,
				observationTrigger(`"comparisons":[]`),
				actionEntity,
			),
		},
		{
			"parameters not an object",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":true}]}`,
				observationTrigger(`"comparisons":[]`),
				actionEntity,
			),
		},
		{
			"whitespace name",
			`{"name":"   ","triggers":[` + observationTrigger(
				`"comparisons":[]`,
			) + `],"steps":[{"id":"s","entity_id":"` + string(
				actionEntity,
			) + `","operation":"set","parameters":{}}]}`,
		},
		{
			"name too long",
			fmt.Sprintf(
				`{"name":%q,"triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
				tooLongName(), observationTrigger(`"comparisons":[]`), actionEntity,
			),
		},
		{"non-object document", `[]`},
		{"empty document", ``},
		{"trailing document", valid + ` false`},
		{
			"definition too large",
			fmt.Sprintf(
				`{"name":"n","triggers":[%s],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{"value":%q}}]}`,
				observationTrigger(`"comparisons":[]`),
				actionEntity,
				tooLongParameter(),
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeDefinition(t, test.raw)
			if err == nil {
				t.Fatal("decoded an invalid definition")
			}
			if !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("error = %v, want ErrInvalidAutomation", err)
			}
		})
	}
}

// Equality operands may be any single JSON value.
func TestDecodeAutomationDefinitionAcceptsEqualityOperandTypes(t *testing.T) {
	t.Parallel()
	observationEntity := newEntityID(t)
	actionEntity := newEntityID(t)
	for _, operand := range []string{`"text"`, `true`, `null`, `[1,2]`, `{"a":1}`, `1e1000`, `-0.5`} {
		raw := fmt.Sprintf(
			`{"name":"n","enabled":true,"triggers":[{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"],"comparisons":[{"pointer":"/value","operator":"eq","operand":%s}]}],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
			observationEntity,
			operand,
			actionEntity,
		)
		if _, err := decodeDefinition(t, raw); err != nil {
			t.Fatalf("operand %s: %v", operand, err)
		}
	}
}

// The embedded schema must compile and require explicit enablement.
func TestEmbeddedDefinitionSchemaCompiles(t *testing.T) {
	t.Parallel()
	codec, err := automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	raw := codec.AutomationDefinitionSchema()
	if len(raw) == 0 {
		t.Fatal("embedded schema is empty")
	}
	var document struct {
		Required []string `json:"required"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"name", "enabled", "triggers", "steps"} {
		if !slices.Contains(document.Required, member) {
			t.Fatalf("schema does not require %q: %v", member, document.Required)
		}
	}
}

// Missing enabled is invalid; explicit false must survive decoding.
func TestDecodeAutomationDefinitionRequiresEnabled(t *testing.T) {
	t.Parallel()
	observationEntity := newEntityID(t)
	actionEntity := newEntityID(t)
	document := func(enabled string) string {
		return fmt.Sprintf(
			`{"name":"n",%s"triggers":[{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"]}],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
			enabled,
			observationEntity,
			actionEntity,
		)
	}
	if _, err := decodeDefinition(t, document("")); err == nil {
		t.Fatal("definition without enabled was accepted")
	} else if !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("error = %v, want ErrInvalidAutomation", err)
	}
	for _, enabled := range []string{`"enabled":true,`, `"enabled":false,`} {
		definition, err := decodeDefinition(t, document(enabled))
		if err != nil {
			t.Fatalf("enabled %s: %v", enabled, err)
		}
		if want := enabled == `"enabled":true,`; definition.Enabled != want {
			t.Fatalf("enabled %s decoded to %v, want %v", enabled, definition.Enabled, want)
		}
	}
}

const (
	tooManyTriggersN    = 33
	tooManyStepsN       = 33
	tooManyComparisonsN = 9
)

// tooLongName returns a name that exceeds the 200-rune bound.
func tooLongName() string { return strings.Repeat("x", 201) }

// tooLongParameter returns one step parameter value that pushes the encoded
// definition past 64 KiB.
func tooLongParameter() string { return strings.Repeat("a", 70*1024) }

// tooManyComparisons renders nine valid comparison objects.
func tooManyComparisons() string {
	entries := make([]string, 0, tooManyComparisonsN)
	for index := range tooManyComparisonsN {
		entries = append(entries, fmt.Sprintf(`{"pointer":"/c%d","operator":"eq","operand":%d}`, index, index))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// tooLongPointer renders one comparison with a 257-byte pointer.
func tooLongPointer() string {
	return fmt.Sprintf(
		`"comparisons":[{"pointer":"/%s","operator":"eq","operand":1}]`,
		strings.Repeat("a", 257),
	)
}

func tooManyTriggers(t *testing.T, entity devices.EntityID) string {
	t.Helper()
	triggers := make([]string, 0, tooManyTriggersN)
	for index := range tooManyTriggersN {
		triggers = append(
			triggers,
			fmt.Sprintf(
				`{"id":"t%d","kind":"observation","entity_id":%q,"dispositions":["applied"]}`,
				index,
				entity,
			),
		)
	}
	return strings.Join(triggers, ",")
}

func tooManySteps(t *testing.T, entity devices.EntityID) string {
	t.Helper()
	steps := make([]string, 0, tooManyStepsN)
	for index := range tooManyStepsN {
		steps = append(
			steps,
			fmt.Sprintf(`{"id":"s%d","entity_id":%q,"operation":"set","parameters":{}}`, index, entity),
		)
	}
	return strings.Join(steps, ",")
}

// typedObservationTrigger builds one valid typed Observation trigger fixture.
func typedObservationTrigger(t *testing.T) *automations.ObservationTrigger {
	t.Helper()
	return &automations.ObservationTrigger{
		EntityID:     newEntityID(t),
		Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
		Comparisons: []automations.ObservationComparison{
			comparison("/temperature", automations.ComparisonGreaterThan, "20"),
		},
	}
}

// typedEntityEventTrigger builds one valid typed Entity Event trigger fixture.
func typedEntityEventTrigger(t *testing.T) *automations.EntityEventTrigger {
	t.Helper()
	return &automations.EntityEventTrigger{EntityID: newEntityID(t), EventName: "single_press"}
}

// Contradictory typed Trigger payloads must be rejected before encoding can
// silently discard a family.
func TestNormalizeAutomationDefinitionRejectsContradictoryTriggerFamilies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		kind   automations.TriggerKind
		mutate func(trigger *automations.AutomationTrigger)
	}{
		{
			"observation carries event payload",
			automations.TriggerKindObservation,
			func(trigger *automations.AutomationTrigger) {
				trigger.EntityEvent = typedEntityEventTrigger(t)
			},
		},
		{
			"observation without observation payload",
			automations.TriggerKindObservation,
			func(trigger *automations.AutomationTrigger) { trigger.Observation = nil },
		},
		{
			"entity event carries observation payload",
			automations.TriggerKindEntityEvent,
			func(trigger *automations.AutomationTrigger) {
				trigger.EntityEvent = typedEntityEventTrigger(t)
				trigger.Observation = typedObservationTrigger(t)
			},
		},
		{
			"entity event without event payload",
			automations.TriggerKindEntityEvent,
			func(trigger *automations.AutomationTrigger) {
				trigger.Observation = nil
				trigger.EntityEvent = nil
			},
		},
		{
			"unknown kind carries a payload",
			automations.TriggerKind("cron"),
			func(trigger *automations.AutomationTrigger) {
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
			_, err := automations.NormalizeAutomationDefinition(definition)
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
	trigger := func(definition *automations.AutomationDefinition, mutate func(*automations.AutomationTrigger)) {
		mutate(&definition.Triggers[0])
	}
	tests := []struct {
		name   string
		mutate func(definition *automations.AutomationDefinition)
	}{
		{"empty name", func(definition *automations.AutomationDefinition) { definition.Name = "" }},
		{"whitespace name", func(definition *automations.AutomationDefinition) { definition.Name = "   " }},
		{
			"untrimmed name beyond bound",
			func(definition *automations.AutomationDefinition) {
				definition.Name = " " + strings.Repeat("x", 200)
			},
		},
		{"no triggers", func(definition *automations.AutomationDefinition) { definition.Triggers = nil }},
		{
			"too many triggers",
			func(definition *automations.AutomationDefinition) {
				definition.Triggers = slices.Repeat(definition.Triggers[:1], tooManyTriggersN)
				for index := range definition.Triggers {
					definition.Triggers[index].ID = automations.TriggerID(fmt.Sprintf("trigger_%d", index))
				}
			},
		},
		{
			"duplicate trigger ids",
			func(definition *automations.AutomationDefinition) {
				definition.Triggers = append(definition.Triggers, definition.Triggers[0])
			},
		},
		{
			"bad trigger slug",
			func(definition *automations.AutomationDefinition) { definition.Triggers[0].ID = "Bad ID" },
		},
		{
			"no dispositions",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Dispositions = nil
				})
			},
		},
		{
			"duplicate dispositions",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Dispositions = []devices.ObservationDisposition{
						devices.DispositionApplied, devices.DispositionApplied,
					}
				})
			},
		},
		{
			"unknown disposition",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Dispositions = []devices.ObservationDisposition{
						devices.ObservationDisposition("rejected"),
					}
				})
			},
		},
		{
			"too many comparisons",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
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
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("temperature", automations.ComparisonEqual, "1"),
					}
				})
			},
		},
		{
			"unknown operator",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonOperator("between"), "1"),
					}
				})
			},
		},
		{
			"ordering operand not numeric",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.Comparisons = []automations.ObservationComparison{
						comparison("/a", automations.ComparisonGreaterThan, `"twenty"`),
					}
				})
			},
		},
		{
			"bad observation entity id",
			func(definition *automations.AutomationDefinition) {
				trigger(definition, func(item *automations.AutomationTrigger) {
					item.Observation.EntityID = "ent_not-a-uuid"
				})
			},
		},
		{
			"bad event name",
			func(definition *automations.AutomationDefinition) {
				definition.Triggers[0] = automations.AutomationTrigger{
					ID: "press", Kind: automations.TriggerKindEntityEvent,
					EntityEvent: &automations.EntityEventTrigger{
						EntityID: newEntityID(t), EventName: "Bad Name",
					},
				}
			},
		},
		{"no steps", func(definition *automations.AutomationDefinition) { definition.Steps = nil }},
		{
			"too many steps",
			func(definition *automations.AutomationDefinition) {
				definition.Steps = slices.Repeat(definition.Steps[:1], tooManyStepsN)
				for index := range definition.Steps {
					definition.Steps[index].ID = automations.StepID(fmt.Sprintf("step_%d", index))
				}
			},
		},
		{
			"duplicate step ids",
			func(definition *automations.AutomationDefinition) {
				definition.Steps = append(definition.Steps, definition.Steps[0])
			},
		},
		{"bad step slug", func(definition *automations.AutomationDefinition) { definition.Steps[0].ID = "S" }},
		{
			"bad operation slug",
			func(definition *automations.AutomationDefinition) { definition.Steps[0].OperationName = "Set It" },
		},
		{
			"bad step entity id",
			func(definition *automations.AutomationDefinition) { definition.Steps[0].EntityID = "ent_not-a-uuid" },
		},
		{
			"parameters not an object",
			func(definition *automations.AutomationDefinition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`true`)
			},
		},
		{
			"parameters are null",
			func(definition *automations.AutomationDefinition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`null`)
			},
		},
		{
			"parameters are invalid json",
			func(definition *automations.AutomationDefinition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`{"value":`)
			},
		},
		{
			"parameters are empty",
			func(definition *automations.AutomationDefinition) { definition.Steps[0].Parameters = nil },
		},
		{
			"parameters carry trailing values",
			func(definition *automations.AutomationDefinition) {
				definition.Steps[0].Parameters = devices.CommandParameters(`{"value":true} false`)
			},
		},
		{
			"encoded definition too large",
			func(definition *automations.AutomationDefinition) {
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
			_, err := automations.NormalizeAutomationDefinition(definition)
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
	decoded, err := automations.DecodeAutomationDefinition(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := automations.NormalizeAutomationDefinition(decoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := automations.EncodeAutomationDefinition(decoded)
	if err != nil {
		t.Fatal(err)
	}
	normalizedRaw, err := automations.EncodeAutomationDefinition(normalized)
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
	definition := automations.AutomationDefinition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.AutomationTrigger{{
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
		Steps: []automations.AutomationStep{{
			ID:            "light_on",
			EntityID:      newEntityID(t),
			OperationName: devices.OperationNameSet,
			Parameters:    parameters,
		}},
	}
	normalized, err := automations.NormalizeAutomationDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	before, err := automations.EncodeAutomationDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	dispositions[0] = devices.DispositionApplied
	operand[1] = 'x'
	parameters[0] = ' '
	definition.Triggers[0].Observation = nil
	after, err := automations.EncodeAutomationDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("normalized definition aliases caller memory:\n%s\n%s", before, after)
	}
}
