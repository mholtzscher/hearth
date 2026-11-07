package automations //nolint:testpackage // Tests exercise the product-owned codec.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutomationArtifactVariantsAndRecovery(t *testing.T) {
	t.Parallel()
	const definition = `{"name":"Reusable","enabled":false,"triggers":[],"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`
	const artifactID = "aart_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const automationID = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	for _, variant := range []Presentation{
		PresentReference{AutomationID: automationID}, PresentCreate{DefinitionJSON: definition},
		PresentReplace{AutomationID: automationID, ExpectedRevision: 2, DefinitionJSON: definition},
	} {
		raw, err := json.Marshal(Artifact{ArtifactID: artifactID, Presentation: variant})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ParseArtifact(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	encoded, err := json.Marshal(
		Artifact{
			ArtifactID:   artifactID,
			Presentation: PresentReplace{AutomationID: automationID, ExpectedRevision: 2, DefinitionJSON: definition},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(string(encoded), `"mode":"replace"`, `"mode":"create"`, 1),
		strings.Replace(string(encoded), `"expected_revision":2`, `"expected_revision":0`, 1),
		strings.Replace(string(encoded), `"kind":"hearth.automation"`, `"kind":"hearth.view"`, 1),
		strings.Replace(string(encoded), `aart_01890f47`, `bad_01890f47`, 1),
		strings.TrimSuffix(string(encoded), "}") + `,"extra":true}`,
		strings.Replace(string(encoded), `"definition_json":`, `"not_definition":`, 1),
	} {
		if _, parseErr := ParseArtifact([]byte(raw)); parseErr == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	future := strings.Replace(string(encoded), `"schema_version":1`, `"schema_version":2`, 1)
	if _, parseErr := ParseArtifact([]byte(future)); parseErr == nil {
		t.Fatal("accepted unsupported new artifact")
	}
	retained, err := ReadArtifact([]byte(future))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(retained)
	if err != nil || string(got) != future {
		t.Fatalf("recovery: %s %v", got, err)
	}
	if _, err = ReadArtifact([]byte(strings.Repeat(" ", 1<<20+1))); err == nil {
		t.Fatal("accepted oversized artifact")
	}
}
