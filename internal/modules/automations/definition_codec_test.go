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
        {"value_pointer": "/temperature", "operator": "gt", "operand": 20},
        {"value_pointer": "", "operator": "eq", "operand": {"occupied": true}}
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

func decodeDefinition(t *testing.T, raw string) (automations.Definition, error) {
	t.Helper()
	return automations.DecodeDefinition(json.RawMessage(raw))
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
	firstRaw, err := automations.EncodeDefinition(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := automations.DecodeDefinition(firstRaw)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw, err := automations.EncodeDefinition(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("round trip changed bytes:\n%s\n%s", firstRaw, secondRaw)
	}
}

// This test protects existing stored definitions during the public field rename
// and fails if the decoder drops the old pointer alias or the encoder restores it.
func TestAutomationDefinitionCanonicalizesLegacyPointer(t *testing.T) {
	t.Parallel()
	raw := definitionFixture(t, newEntityID(t), newEntityID(t), newEntityID(t))
	raw = strings.ReplaceAll(raw, `"value_pointer"`, `"pointer"`)
	definition, err := decodeDefinition(t, raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"pointer"`) {
		t.Fatalf("encoded definition retained legacy pointer: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"value_pointer"`) {
		t.Fatalf("encoded definition omitted value_pointer: %s", encoded)
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
			triggers(observationTrigger(
				`"comparisons":[{"value_pointer":"/a","operator":"eq","operand":1,"extra":1}]`,
			)),
		},
		{
			"unknown kind",
			triggers(
				fmt.Sprintf(`{"id":"t","kind":"unknown","entity_id":%q,"dispositions":["applied"]}`, observationEntity),
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
			triggers(observationTrigger(`"comparisons":[{"value_pointer":"temperature","operator":"eq","operand":1}]`)),
		},
		{
			"pointer bad escape",
			triggers(observationTrigger(
				`"comparisons":[{"value_pointer":"/bad~2escape","operator":"eq","operand":1}]`,
			)),
		},
		{
			"pointer trailing escape",
			triggers(observationTrigger(`"comparisons":[{"value_pointer":"/trailing~","operator":"eq","operand":1}]`)),
		},
		{"pointer too long", triggers(observationTrigger(tooLongPointer()))},
		{
			"ordering operand not numeric",
			triggers(observationTrigger(`"comparisons":[{"value_pointer":"/a","operator":"gt","operand":"twenty"}]`)),
		},
		{
			"unknown operator",
			triggers(observationTrigger(`"comparisons":[{"value_pointer":"/a","operator":"between","operand":1}]`)),
		},
		{"missing operand", triggers(observationTrigger(`"comparisons":[{"value_pointer":"/a","operator":"eq"}]`))},
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
			`{"name":"n","enabled":true,"triggers":[{"id":"t","kind":"observation","entity_id":%q,"dispositions":["applied"],"comparisons":[{"value_pointer":"/value","operator":"eq","operand":%s}]}],"steps":[{"id":"s","entity_id":%q,"operation":"set","parameters":{}}]}`,
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
	codec, err := automations.NewDefinitionCodec()
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
		entries = append(entries, fmt.Sprintf(`{"value_pointer":"/c%d","operator":"eq","operand":%d}`, index, index))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// tooLongPointer renders one comparison with a 257-byte pointer.
func tooLongPointer() string {
	return fmt.Sprintf(
		`"comparisons":[{"value_pointer":"/%s","operator":"eq","operand":1}]`,
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

func cronDocument(t *testing.T, trigger string) string {
	t.Helper()
	return fmt.Sprintf(
		`{"name":"Scheduled","enabled":true,"triggers":[%s],"steps":[{"id":"step","entity_id":%q,"operation":"set","parameters":{}}]}`,
		trigger,
		newEntityID(t),
	)
}

// A1's restricted language must hold at both raw JSON and typed boundaries.
// Several cases deliberately exercise syntax robfig accepts more loosely.
func TestCronDefinitionRejectsUnrestrictedExpressions(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		"", " \t\n", "0 7 * *", "0 0 7 * * *", "0 7 * * * 2026",
		"0 7 1 * *", "0 7 */1 * *", "0 7 1-31 * *", "0 7 * */1 *", "0 7 * 1-12 *",
		"@daily", "@every 1m", "TZ=UTC 0 7 * * *", "CRON_TZ=UTC 0 7 * * *",
		"? 7 * * *", "0 7 * * ?", "0 7 * * L", "0 7 * * W", "0 7 * * MON#2",
		"60 7 * * *", "0 24 * * *", "0 7 * * 7", "0 7 * * 0-7", "0 7 * * SAT-SUN",
		"0 7 * * MONDAY", "0 7 * * XYZ", "*/0 7 * * *", "0 7 * * MON/0",
		"/15 7 * * *", "+1 7 * * *", "-1 7 * * *", "0 7 * * +1",
		"1,,2 7 * * *", ",1 7 * * *", "1, 7 * * *", "0 7 * * MON,,FRI",
		"1-*/2 7 * * *", "*-5 7 * * *", "5-1 7 * * *", "1-2-3 7 * * *",
		"*/ 7 * * *", "*/+2 7 * * *", "1/2/3 7 * * *", "0 7 * * *!",
		strings.Repeat(" ", 504) + "0 7 * * *",      // 513 bytes before normalization.
		strings.Repeat("\u2003", 168) + "0 7 * * *", // Byte, not rune, bound.
	} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			definition := cronDefinition(t, expression)
			if _, err := automations.NormalizeDefinition(
				definition,
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("typed error = %v, want invalid automation", err)
			}
			raw := cronDocument(t, fmt.Sprintf(`{"id":"scheduled","kind":"cron","expression":%q}`, expression))
			if _, err := decodeDefinition(t, raw); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("JSON error = %v, want invalid automation", err)
			}
		})
	}
}

func TestCronDefinitionRejectsInvalidJSONFamilies(t *testing.T) {
	t.Parallel()
	triggers := []string{
		`{"id":"scheduled","kind":"cron"}`,
		`{"id":"scheduled","kind":"cron","expression":null}`,
		`{"id":"scheduled","kind":"cron","expression":15}`,
		`{"id":"scheduled","kind":"cron","expression":true}`,
		`{"id":"scheduled","kind":"clock_time","expression":"0 7 * * *"}`,
		`{"id":"scheduled","kind":"time_pattern","expression":"0 7 * * *"}`,
		`{"id":"event","kind":"entity_event","event_name":"press"}`,
		`{"id":"observation","kind":"observation","dispositions":["applied"]}`,
		`{"id":"held","kind":"held_state","comparisons":[{"value_pointer":"","operator":"eq","operand":true}],"for_seconds":60}`,
	}
	for _, field := range []string{"entity_id", "local_time", "weekdays", "hours", "minutes", "seconds", "comparisons", "previous_comparisons", "dispositions", "for_seconds", "event_name", "extra"} {
		triggers = append(
			triggers,
			fmt.Sprintf(`{"id":"scheduled","kind":"cron","expression":"0 7 * * *",%q:null}`, field),
		)
	}
	for _, trigger := range triggers {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeDefinition(
				t,
				cronDocument(t, trigger),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("error = %v, want invalid automation", err)
			}
			if _, err := automations.DecodeMatchedTriggers(
				json.RawMessage("[" + trigger + "]"),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("snapshot error = %v, want invalid automation", err)
			}
		})
	}
}

func TestCronDefinitionCodecNormalizesAndPreservesTokens(t *testing.T) {
	t.Parallel()
	input := "\t00\t0,23\n*  *\tSuN,FRI  "
	definition, err := decodeDefinition(
		t,
		cronDocument(t, fmt.Sprintf(`{"id":"scheduled","kind":"cron","expression":%q}`, input)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := definition.Triggers[0].Cron.Expression; got != "00 0,23 * * SuN,FRI" {
		t.Fatalf("expression = %q", got)
	}
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Triggers []map[string]json.RawMessage `json:"triggers"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Triggers[0]) != 3 || string(document.Triggers[0]["expression"]) != `"00 0,23 * * SuN,FRI"` {
		t.Fatalf("cron JSON = %s", raw)
	}
	snapshots, err := automations.MatchedTriggerSnapshots(definition, []automations.TriggerID{"scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	definition.Triggers[0].Cron.Expression = "* * * * *"
	snapshotRaw, err := automations.EncodeMatchedTriggers(snapshots)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeMatchedTriggers(snapshotRaw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded[0].Cron.Expression != "00 0,23 * * SuN,FRI" {
		t.Fatalf("snapshot = %s", snapshotRaw)
	}
	// The pre-normalization bound accepts exactly 512 bytes.
	if _, err = automations.NormalizeDefinition(cronDefinition(t, strings.Repeat(" ", 503)+"0 7 * * *")); err != nil {
		t.Fatalf("512-byte expression: %v", err)
	}
}

// Contradictory typed Trigger payloads must be rejected before encoding can
// silently discard a family.
