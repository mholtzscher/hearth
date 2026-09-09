package automations //nolint:testpackage // Tests inspect parser masks and inject repository clocks/failures.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

//nolint:gocognit // Keep the complete contract scenario and independent assertions together.
func TestAutomationDefinitionFixtures(t *testing.T) {
	t.Parallel()
	codec, compileErr := NewAutomationDefinitionCodec()
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	for _, name := range []string{"valid", "unknown-field", "singular-trigger", "nonobject-parameters"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile("testdata/automation-definitions/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			definition, err := codec.DecodeAutomationDefinition(raw)
			if name != "valid" {
				var validation *AutomationDefinitionValidationError
				if !errors.As(err, &validation) || len(validation.Issues) == 0 {
					t.Fatalf("expected schema issues, got %v", err)
				}
				if strings.Contains(err.Error(), "ent_") {
					t.Fatal("payload leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if definition.Enabled || definition.Name != "Evening lights" ||
				definition.Triggers[0].Expression != "0 19 * * 1-5" {
				t.Fatalf("normalization: %+v", definition)
			}
			if !bytes.Contains(definition.Steps[0].Parameters, []byte("9007199254740993")) ||
				!bytes.Contains(definition.Steps[0].Parameters, []byte("0.1234567890123456789")) {
				t.Fatal("numeric precision lost")
			}
			if err = codec.ValidateAutomationDefinition(definition); err != nil {
				t.Fatal(err)
			}
			schema := codec.AutomationDefinitionSchema()
			schema[0] = '!'
			if codec.AutomationDefinitionSchema()[0] != '{' {
				t.Fatal("schema aliases caller bytes")
			}
			clear(raw)
			if !bytes.Contains(definition.Steps[0].Parameters, []byte("9007199254740993")) {
				t.Fatal("definition aliases raw bytes")
			}
		})
	}
}

//nolint:gocognit // Keep the complete contract scenario and independent assertions together.
func TestAutomationDefinitionStructuralBoundaries(t *testing.T) {
	t.Parallel()
	codec, compileErr := NewAutomationDefinitionCodec()
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	base := `{"name":"test","triggers":[{"id":"daily","kind":"cron","expression":"0 19 * * *"}],"steps":[{"entity_id":"ent_x","operation_name":"set","parameters":{}}]}`
	cases := map[string]string{
		"missing name":       strings.Replace(base, `"name":"test",`, "", 1),
		"slug":               strings.Replace(base, `"daily"`, `"Uppercase"`, 1),
		"kind":               strings.Replace(base, `"cron"`, `"event"`, 1),
		"trigger extra":      strings.Replace(base, `"id":"daily"`, `"extra":true,"id":"daily"`, 1),
		"step extra":         strings.Replace(base, `"entity_id"`, `"extra":true,"entity_id"`, 1),
		"missing parameters": strings.Replace(base, `,"parameters":{}`, "", 1),
		"null parameters":    strings.Replace(base, `"parameters":{}`, `"parameters":null`, 1),
		"array parameters":   strings.Replace(base, `"parameters":{}`, `"parameters":[]`, 1),
		"trailing":           base + ` {}`,
		"malformed":          `{`,
		"size": strings.Replace(
			base,
			`"parameters":{}`,
			`"parameters":{"x":"`+strings.Repeat("a", 65536)+`"}`,
			1,
		),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.DecodeAutomationDefinition([]byte(raw)); err == nil {
				t.Fatal("accepted invalid structure")
			}
		})
	}
	for _, count := range []int{0, 1, 32, 33} {
		var document map[string]json.RawMessage
		if err := json.Unmarshal([]byte(base), &document); err != nil {
			t.Fatal(err)
		}
		triggers := make([]json.RawMessage, count)
		for i := range triggers {
			triggers[i] = json.RawMessage(`{"id":"daily","kind":"cron","expression":"0 19 * * *"}`)
		}
		encodedTriggers, err := json.Marshal(triggers)
		if err != nil {
			t.Fatal(err)
		}
		document["triggers"] = encodedTriggers
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		_, err = codec.DecodeAutomationDefinition(raw)
		if (err == nil) != (count == 1 || count == 32) {
			t.Fatalf("trigger count %d: %v", count, err)
		}
	}
	for _, count := range []int{0, 1, 100, 101} {
		definition := testAutomationDefinition()
		definition.Steps = make([]AutomationStep, count)
		for i := range count {
			definition.Steps[i] = testAutomationDefinition().Steps[0]
		}
		err := codec.ValidateAutomationDefinition(definition)
		if (err == nil) != (count == 1 || count == 100) {
			t.Fatalf("step count %d: %v", count, err)
		}
	}
}

func TestAutomationPowerAuthoringFixture(t *testing.T) {
	t.Parallel()
	codec, err := NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/automation-definitions/valid-power.json")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := codec.DecodeAutomationDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Triggers) != 2 || definition.Triggers[0].ID != "weekdays" ||
		definition.Triggers[1].ID != "weekends" ||
		len(definition.Steps) != 1 {
		t.Fatal("authoring fixture order changed")
	}
	if err = codec.ValidateAutomationDefinition(definition); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationDefinitionIssuesUseJSONPointers(t *testing.T) {
	t.Parallel()
	codec, err := NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/automation-definitions/nonobject-parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = codec.DecodeAutomationDefinition(raw)
	var validation *AutomationDefinitionValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected schema issues: %v", err)
	}
	if len(validation.Issues) != 1 || validation.Issues[0].Path != "/steps/0/parameters" {
		t.Fatalf("expected safe JSON pointer: %+v", validation.Issues)
	}
}
