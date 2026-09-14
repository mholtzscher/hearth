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

// TestDecodeAutomationDefinitionNormalizesValidDocuments protects the strict
// persisted shape: a valid document decodes with a trimmed name, canonical
// disposition order, typed family payloads, and ordered Steps. It fails if
// normalization drops or reorders identity-bearing fields.
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

// TestDecodeAutomationDefinitionIsStableUnderRoundTrip protects the canonical
// persisted representation: decode(normalize(definition)) encodes to identical
// bytes on a second pass. It fails on unstable ordering or omitted defaults.
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

// TestDecodeAutomationDefinitionRejectsInvalidDocuments protects every strict
// structural rule in §3.1 and §6.1 as a permanent input error. Each case fails
// if a malformed or partially validated definition can reach persistence.
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

// TestDecodeAutomationDefinitionAcceptsEqualityOperandTypes protects the
// documented eq/ne operand freedom: any single JSON value is accepted, while
// ordering operators remain numeric-only.
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

// TestEmbeddedDefinitionSchemaCompiles protects the embedded persisted shape
// against an unparseable or unresolvable schema, which would otherwise surface
// as every definition failing validation at runtime. It also pins the explicit
// enabled member: the schema must require it so enablement can never default.
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

// TestDecodeAutomationDefinitionRequiresEnabled protects the explicit enabled
// state rule in §3.1: omitting enabled is a permanent input error, while an
// explicit false is accepted and preserved. It fails if enablement can default
// silently to false.
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
