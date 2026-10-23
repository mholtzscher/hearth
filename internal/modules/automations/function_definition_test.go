package automations_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func functionDescriptor() adapters.Descriptor {
	return adapters.Descriptor{
		Key:         "inspect",
		Description: "Inspect a fixed object",
		TimeoutMS:   1000,
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`,
		),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func functionNode(t *testing.T, id string) automations.Step {
	t.Helper()
	fingerprint, err := adapters.FingerprintDescriptor(functionDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	return automations.Step{ID: automations.StepID(id), Body: automations.FunctionStep{
		AdapterID: "test-functions", FunctionKey: "inspect", ContractFingerprint: fingerprint,
		Arguments: json.RawMessage(`{"value":9007199254740993}`)}}
}

func TestFunctionDefinitionNestedExactRoundTrip(t *testing.T) {
	t.Parallel()
	definition := branchingFixture(t)
	command := definition.Steps[0]
	definition.Steps = []automations.Step{functionNode(t, "top"), {ID: "if", Body: automations.IfStep{
		Conditions: triggerPredicate("a"), Then: []automations.Step{functionNode(t, "then")},
		Else: []automations.Step{command}}}}
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := automations.EncodeDefinition(decoded)
	if err != nil || !bytes.Equal(raw, encoded) || !bytes.Contains(encoded, []byte(`9007199254740993`)) {
		t.Fatalf("Function round trip = %s, %v", encoded, err)
	}
	if leaves := automations.CommandLeaves(decoded.Steps); len(leaves) != 1 || leaves[0].ID != command.ID {
		t.Fatalf("Function shifted Command leaves: %#v", leaves)
	}
	if decoded.Steps[0].Kind() != automations.StepKindFunction {
		t.Fatal("Function kind lost")
	}
}

func TestFunctionDefinitionStructuralBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*automations.FunctionStep)
	}{
		{"Adapter", func(step *automations.FunctionStep) { step.AdapterID = "invalid.adapter" }},
		{"key", func(step *automations.FunctionStep) { step.FunctionKey = "Invalid" }},
		{"fingerprint", func(step *automations.FunctionStep) { step.ContractFingerprint = "sha256-v1:wrong" }},
		{"array", func(step *automations.FunctionStep) { step.Arguments = json.RawMessage(`[]`) }},
		{"null", func(step *automations.FunctionStep) { step.Arguments = json.RawMessage(`null`) }},
		{"duplicate keys", func(step *automations.FunctionStep) { step.Arguments = json.RawMessage(`{"value":1,"value":2}`) }},
		{"depth", func(step *automations.FunctionStep) {
			step.Arguments = json.RawMessage(`{"v":` + strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17) + "}")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := branchingFixture(t)
			node := functionNode(t, "function")
			body := node.Body.(automations.FunctionStep)
			test.mutate(&body)
			node.Body = body
			definition.Steps = []automations.Step{node}
			if _, err := automations.NormalizeDefinition(definition); err == nil {
				t.Fatal("invalid Function accepted")
			}
		})
	}
	definition := branchingFixture(t)
	definition.Steps = nil
	for index := range 32 {
		definition.Steps = append(definition.Steps, functionNode(t, fmt.Sprintf("f-%d", index)))
	}
	if _, err := automations.NormalizeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	definition.Steps[0] = automations.Step{ID: "branch", Body: automations.IfStep{
		Conditions: triggerPredicate(
			"a",
		),
		Then: []automations.Step{functionNode(t, "extra-a"), notificationNode("extra-b")},
	}}
	if _, err := automations.NormalizeDefinition(definition); err == nil {
		t.Fatal("33 mixed side effects accepted")
	}
	definition.Steps = []automations.Step{functionNode(t, "huge")}
	body := definition.Steps[0].Body.(automations.FunctionStep)
	body.Arguments = json.RawMessage(`{"value":"` + strings.Repeat("x", 65536) + `"}`)
	definition.Steps[0].Body = body
	if _, err := automations.NormalizeDefinition(definition); err == nil {
		t.Fatal("oversized arguments accepted")
	}
}
