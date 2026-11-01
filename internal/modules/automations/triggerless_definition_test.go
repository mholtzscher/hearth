package automations_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestTriggerlessDefinitionCodec(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`,
	)
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Triggers == nil || len(definition.Triggers) != 0 {
		t.Fatalf("triggers = %#v, want empty slice", definition.Triggers)
	}
	definition.Triggers = nil
	encoded, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"triggers":[]`)) {
		t.Fatalf("encoded definition = %s, want empty trigger array", encoded)
	}
	for _, invalid := range []string{
		`{"name":"Reusable","enabled":false,"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`,
		`{"name":"Reusable","enabled":false,"triggers":null,"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`,
	} {
		if _, err = automations.DecodeDefinition(json.RawMessage(invalid)); err == nil {
			t.Errorf("DecodeDefinition(%s) accepted invalid triggers", invalid)
		}
	}
}
