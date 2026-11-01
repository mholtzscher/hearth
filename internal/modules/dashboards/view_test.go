package dashboards_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestSharedExamples(t *testing.T) {
	t.Parallel()
	var examples []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			d, err := dashboards.ParseDefinition(example.Definition)
			if (err == nil) != example.Valid {
				t.Fatalf("valid=%v err=%v", example.Valid, err)
			}
			if example.Valid {
				reader := entityReader{
					entity: devices.EntityWithState{
						Entity: devices.Entity{
							TypeID:  "hearth.temperature/v1",
							Support: devices.EntitySupport(`{"state":{"unit":"mCel"},"operations":{}}`),
						},
					},
				}
				if _, presentErr := dashboards.NewService(nil, reader).
					PresentView(context.Background(), d); presentErr != nil {
					t.Fatal(presentErr)
				}
			}
			if _, readErr := dashboards.ReadDefinition(example.Definition); readErr != nil {
				t.Fatalf("recoverable: %v", readErr)
			}
		})
	}
}

func TestReadDefinitionChecksEnvelopeButPreservesInvalidGraph(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"schema_version":1,"catalog_version":"future","title":123,"spec":{}}`,
		`{"schema_version":"1","catalog_version":"future","title":"x","spec":{}}`,
		`{"schema_version":1,"catalog_version":{},"title":"x","spec":{}}`,
	} {
		if _, err := dashboards.ReadDefinition(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw := json.RawMessage(`{"schema_version":2,"catalog_version":"future","title":"Future","spec":[],"future":true}`)
	got, err := dashboards.ReadDefinition(raw)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("read: %s %v", got, err)
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr == nil {
		t.Fatal("invalid graph accepted for writes")
	}
	oversize := json.RawMessage(
		`{"schema_version":1,"catalog_version":"x","title":"` + strings.Repeat(
			"x",
			dashboards.MaxDefinitionBytes,
		) + `","spec":{}}`,
	)
	if _, readErr := dashboards.ReadDefinition(oversize); readErr == nil {
		t.Fatal("oversize retained definition accepted")
	}
	if _, parseErr := dashboards.ParseDefinition(oversize); parseErr == nil {
		t.Fatal("oversize write accepted")
	}
}

func TestDefinitionSizeLimitWithOtherwiseValidUnicodeNodes(t *testing.T) {
	t.Parallel()
	children := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	elements := map[string]any{
		"root": map[string]any{"type": "Stack", "props": map[string]any{"gap": "small"}, "children": children},
	}
	for _, key := range children {
		elements[key] = map[string]any{"type": "Note", "props": map[string]any{"text": strings.Repeat("🌡", 480)}}
	}
	value := map[string]any{
		"schema_version":  1,
		"catalog_version": "hearth.dashboard/v1",
		"title":           "Size",
		"spec":            map[string]any{"root": "root", "elements": elements},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= dashboards.MaxDefinitionBytes {
		t.Fatalf("fixture is not oversized: %d", len(raw))
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr == nil {
		t.Fatal("oversized valid graph accepted")
	}
	children = children[:8]
	elements["root"].(map[string]any)["children"] = children
	delete(elements, "i")
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, parseErr := dashboards.ParseDefinition(raw); parseErr != nil {
		t.Fatalf("under-limit control: %v", parseErr)
	}
}

func TestSharedReferenceLimitFixtureHasValidSixteenEntityControl(t *testing.T) {
	t.Parallel()
	var examples []struct {
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		if example.Name != "too-many-entity-references" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(example.Definition, &value); err != nil {
			t.Fatal(err)
		}
		elements := value["spec"].(map[string]any)["elements"].(map[string]any)
		delete(elements, "c")
		elements["r"].(map[string]any)["children"] = []string{"a", "b"}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, parseErr := dashboards.ParseDefinition(raw); parseErr != nil {
			t.Fatalf("16-reference control: %v", parseErr)
		}
		return
	}
	t.Fatal("missing shared reference fixture")
}
