package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// conditionDefinitionJSON builds one otherwise valid definition document with an
// optional conditions member. An empty conditionsField omits the member.
func conditionDefinitionJSON(entity devices.EntityID, conditionsField string) string {
	conditions := ""
	if conditionsField != "" {
		conditions = `"conditions": ` + conditionsField + ","
	}
	return fmt.Sprintf(`{
  "name": "Conditional light",
  "enabled": true,
  %s
  "triggers": [
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
}`, conditions, entity, entity)
}

func conditionObjectJSON(entity devices.EntityID) string {
	return fmt.Sprintf(`{
    "id": "lighting-allowed",
    "kind": "all",
    "children": [
      {
        "id": "room-dark",
        "kind": "entity_state",
        "entity_id": %q,
        "value_pointer": "",
        "operator": "lt",
        "operand": 30,
        "max_age_seconds": 300
      },
      {
        "id": "other-room-unoccupied",
        "kind": "not",
        "child": {
          "id": "other-room-occupied",
          "kind": "entity_state",
          "entity_id": %q,
          "value_pointer": "",
          "operator": "eq",
          "operand": true
        }
      }
    ]
  }`, entity, entity)
}

// nestedNotConditionObject returns a not chain with notCount not nodes plus one
// entity_state leaf, so the node count is notCount+1 and the depth is notCount+1.
func nestedNotConditionObject(entity devices.EntityID, notCount int) string {
	value := fmt.Sprintf(
		`{"id":"n%d","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":true}`,
		notCount, entity,
	)
	for level := notCount - 1; level >= 0; level-- {
		value = fmt.Sprintf(`{"id":"n%d","kind":"not","child":%s}`, level, value)
	}
	return value
}

// wideConditionObject returns an all group with childCount entity_state leaves,
// so the node count is childCount+1.
func wideConditionObject(entity devices.EntityID, childCount int) string {
	children := make([]string, 0, childCount)
	for index := range childCount {
		children = append(children, fmt.Sprintf(
			`{"id":"c%d","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":true}`,
			index, entity,
		))
	}
	return `{"id":"root","kind":"all","children":[` + strings.Join(children, ",") + `]}`
}

func conditionLeafJSON(entity devices.EntityID, fields string) string {
	return fmt.Sprintf(
		`{"id":"room-dark","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":true%s}`,
		entity, fields,
	)
}

// Omission round-trips without adding Conditions; explicit JSON null is invalid.
func TestDecodeAutomationDefinitionConditionsOmissionAndNull(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	omitted, err := decodeDefinition(t, conditionDefinitionJSON(entity, ""))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Conditions != nil {
		t.Fatalf("omitted conditions = %#v, want nil", omitted.Conditions)
	}
	raw, err := automations.EncodeDefinition(omitted)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"conditions"`)) {
		t.Fatalf("encoding added conditions: %s", raw)
	}
	if _, err = decodeDefinition(
		t,
		conditionDefinitionJSON(entity, "null"),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("explicit null conditions error = %v, want ErrInvalidAutomation", err)
	}
}

// A valid Condition tree decodes, normalizes, and round-trips stably.
func TestDecodeAutomationDefinitionConditionsRoundTrip(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	definition, err := decodeDefinition(t, conditionDefinitionJSON(entity, conditionObjectJSON(entity)))
	if err != nil {
		t.Fatal(err)
	}
	root := definition.Conditions
	if root == nil || root.Kind != automations.ConditionAll || len(root.Children) != 2 {
		t.Fatalf("conditions = %#v", root)
	}
	leaf := root.Children[0]
	if leaf.Kind != automations.ConditionEntityState || leaf.EntityState == nil ||
		leaf.EntityState.MaxAgeSeconds == nil || *leaf.EntityState.MaxAgeSeconds != 300 {
		t.Fatalf("leaf = %#v", leaf)
	}
	if leaf.EntityState.Pointer != "" {
		t.Fatalf("empty pointer = %q, want preserved", leaf.EntityState.Pointer)
	}
	not := root.Children[1]
	if not.Kind != automations.ConditionNot || not.Child == nil ||
		not.Child.ID != "other-room-occupied" {
		t.Fatalf("not node = %#v", not)
	}
	encoded, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	second, err := decodeDefinition(t, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	again, err := automations.EncodeDefinition(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatalf("round trip not stable:\n%s\n%s", encoded, again)
	}
}

// This test protects stored Condition trees written before value_pointer was
// introduced and fails if they become unreadable or remain legacy-shaped.
func TestConditionDefinitionCanonicalizesLegacyPointer(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	conditions := strings.ReplaceAll(conditionObjectJSON(entity), `"value_pointer"`, `"pointer"`)
	definition, err := decodeDefinition(t, conditionDefinitionJSON(entity, conditions))
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

// Every strict family, bound, and shape violation is rejected as a permanent
// invalid definition.
func TestDecodeAutomationDefinitionConditionsRejections(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	cases := []struct {
		name       string
		conditions string
	}{
		{"explicit null", "null"},
		{"empty all", `{"id":"g","kind":"all","children":[]}`},
		{"empty any", `{"id":"g","kind":"any","children":[]}`},
		{
			"entity state with children",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":true,"children":[%s]}`,
				entity,
				conditionLeafJSON(entity, ""),
			),
		},
		{
			"all with entity_id",
			fmt.Sprintf(
				`{"id":"g","kind":"all","entity_id":%q,"children":[%s]}`, entity, conditionLeafJSON(entity, ""),
			),
		},
		{
			"not with children",
			fmt.Sprintf(`{"id":"g","kind":"not","children":[%s]}`, conditionLeafJSON(entity, "")),
		},
		{
			"not without child",
			`{"id":"g","kind":"not"}`,
		},
		{
			"duplicate IDs",
			`{"id":"g","kind":"all","children":[` + conditionLeafJSON(entity, "") + `,` +
				conditionLeafJSON(entity, "") + `]}`,
		},
		{"non-slug ID", `{"id":"Root","kind":"not","child":` + conditionLeafJSON(entity, "") + `}`},
		{"unknown kind", `{"id":"g","kind":"xor","children":[` + conditionLeafJSON(entity, "") + `]}`},
		{"unknown field", `{"id":"g","kind":"not","child":` + conditionLeafJSON(entity, "") + `,"extra":1}`},
		{"depth 9", nestedNotConditionObject(entity, 8)},
		{"node 65", wideConditionObject(entity, 64)},
		{"age zero", conditionLeafJSON(entity, `,"max_age_seconds":0`)},
		{"age above maximum", conditionLeafJSON(entity, `,"max_age_seconds":2592001`)},
		{
			"pointer without slash",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"a","operator":"eq","operand":true}`,
				entity,
			),
		},
		{
			"unknown operator",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"like","operand":true}`,
				entity,
			),
		},
		{
			"missing operand",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq"}`, entity,
			),
		},
		{
			"ordering operand not numeric",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"lt","operand":"30"}`,
				entity,
			),
		},
		{
			"operand is not one JSON value",
			fmt.Sprintf(
				`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":true false}`,
				entity,
			),
		},
	}
	for _, testCase := range cases {
		if _, err := decodeDefinition(t, conditionDefinitionJSON(entity, testCase.conditions)); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// The exact boundaries are accepted: depth 8, node 64, and both age limits.
func TestDecodeAutomationDefinitionConditionsAcceptsBoundaries(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	cases := []struct {
		name       string
		conditions string
	}{
		{"depth 8", nestedNotConditionObject(entity, 7)},
		{"node 64", wideConditionObject(entity, 63)},
		{"age minimum", conditionLeafJSON(entity, `,"max_age_seconds":1`)},
		{"age maximum", conditionLeafJSON(entity, `,"max_age_seconds":2592000`)},
	}
	for _, testCase := range cases {
		if _, err := decodeDefinition(t, conditionDefinitionJSON(entity, testCase.conditions)); err != nil {
			t.Errorf("%s: %v", testCase.name, err)
		}
	}
}

// A normalized definition that exceeds 64 KiB is rejected even when its tree is
// structurally valid.
func TestDecodeAutomationDefinitionConditionsRejectsOversizeDefinition(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	huge := fmt.Sprintf(
		`{"id":"s","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":%q}`,
		entity, strings.Repeat("a", 70_000),
	)
	if _, err := decodeDefinition(t, conditionDefinitionJSON(entity, huge)); !errors.Is(
		err, automations.ErrInvalidAutomation,
	) {
		t.Fatalf("oversize definition error = %v, want ErrInvalidAutomation", err)
	}
}

// A typed cycle is rejected through the definition codec without runaway
// recursion.
func TestNormalizeAutomationDefinitionRejectsTypedConditionCycle(t *testing.T) {
	t.Parallel()
	triggerEntity := newEntityID(t)
	stepEntity := newEntityID(t)
	cycle := automations.Condition{ID: "cycle", Kind: automations.ConditionNot}
	cycle.Child = &cycle
	conditions := conditionNot("root", cycle)
	definition := automations.Definition{
		Name:    "Cyclic",
		Enabled: true,
		Triggers: []automations.Trigger{
			{
				ID: "press", Kind: automations.TriggerKindEntityEvent,
				EntityEvent: &automations.EntityEventTrigger{EntityID: triggerEntity, EventName: "single_press"},
			},
		},
		Conditions: &conditions,
		Steps: []automations.Step{
			{
				ID: "light_on", EntityID: stepEntity, OperationName: devices.OperationNameSet,
				Parameters: devices.CommandParameters(`{"value":true}`),
			},
		},
	}
	if _, err := automations.NormalizeDefinition(definition); !errors.Is(
		err, automations.ErrInvalidAutomation,
	) {
		t.Fatalf("typed cycle error = %v, want ErrInvalidAutomation", err)
	}
}

// Save-time reference validation checks every Condition Entity through the
// devices seam and maps a permanent condition-reference failure to
// ErrInvalidAutomation without widening Trigger semantics.
func TestValidateAutomationDefinitionValidatesConditionEntities(t *testing.T) {
	t.Parallel()
	stub := &stubAutomationDevices{}
	triggerEntity := newEntityID(t)
	stepEntity := newEntityID(t)
	conditionEntityID := newEntityID(t)
	conditions := conditionLeaf("room-dark", conditionEntityID, "", automations.ComparisonEqual, "true", nil)
	definition := automations.Definition{
		Name:    "Conditional",
		Enabled: true,
		Triggers: []automations.Trigger{
			{
				ID: "press", Kind: automations.TriggerKindEntityEvent,
				EntityEvent: &automations.EntityEventTrigger{EntityID: triggerEntity, EventName: "single_press"},
			},
		},
		Conditions: &conditions,
		Steps: []automations.Step{
			{
				ID: "light_on", EntityID: stepEntity, OperationName: devices.OperationNameSet,
				Parameters: devices.CommandParameters(`{"value":true}`),
			},
		},
	}
	if _, err := automations.ValidateDefinition(
		context.Background(), stub, definition,
	); err != nil {
		t.Fatalf("valid definition: %v", err)
	}
	if len(stub.conditionCalls) != 1 || stub.conditionCalls[0] != conditionEntityID {
		t.Fatalf("condition validation calls = %v, want the leaf Entity", stub.conditionCalls)
	}

	stub.conditionError = errors.New("entity has no State")
	if _, err := automations.ValidateDefinition(
		context.Background(), stub, definition,
	); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("condition reference failure = %v, want ErrInvalidAutomation", err)
	}

	stub.conditionError = nil
	stub.conditionCalls = nil
	unconditioned := definition
	unconditioned.Conditions = nil
	if _, err := automations.ValidateDefinition(
		context.Background(), stub, unconditioned,
	); err != nil {
		t.Fatalf("unconditioned definition: %v", err)
	}
	if len(stub.conditionCalls) != 0 {
		t.Fatalf("unconditioned definition validated %d condition entities", len(stub.conditionCalls))
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

// Fuzzing the bounded definition decoder must never panic, and every accepted
// document must survive an encode/decode cycle unchanged.
func FuzzDecodeAutomationDefinitionConditions(fuzz *testing.F) {
	const entity devices.EntityID = "ent_01890f47-7a6b-7c4d-8e9f-000000000001"
	fuzz.Add(conditionDefinitionJSON(entity, conditionObjectJSON(entity)))
	fuzz.Add(conditionDefinitionJSON(entity, ""))
	fuzz.Add(conditionDefinitionJSON(entity, "null"))
	fuzz.Add(conditionDefinitionJSON(entity, nestedNotConditionObject(entity, 7)))
	fuzz.Fuzz(func(t *testing.T, raw string) {
		definition, err := automations.DecodeDefinition(json.RawMessage(raw))
		if err != nil {
			return
		}
		encoded, err := automations.EncodeDefinition(definition)
		if err != nil {
			t.Fatalf("accepted definition failed to encode: %v", err)
		}
		redecoded, err := automations.DecodeDefinition(encoded)
		if err != nil {
			t.Fatalf("accepted definition failed to re-decode: %v", err)
		}
		again, err := automations.EncodeDefinition(redecoded)
		if err != nil {
			t.Fatalf("re-decoded definition failed to encode: %v", err)
		}
		if !bytes.Equal(encoded, again) {
			t.Fatalf("canonical encoding is not stable:\n%s\n%s", encoded, again)
		}
	})
}
