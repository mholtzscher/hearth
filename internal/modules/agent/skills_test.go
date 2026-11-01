package agent //nolint:testpackage // The embedded catalog is private to the agent module.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestEmbeddedSkillsListAndGet(t *testing.T) {
	t.Parallel()
	backend, err := newEmbeddedSkills()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := backend.List(context.Background())
	if err != nil || len(metadata) != 1 {
		t.Fatalf("List() = %v, %v; want one skill", metadata, err)
	}
	got, err := backend.Get(context.Background(), automationSkillName)
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontMatter != metadata[0] || got.Name != automationSkillName || got.Description == "" ||
		strings.TrimSpace(got.Content) == "" {
		t.Fatalf("unexpected skill: %+v", got)
	}
	if got.Context != "" || got.Agent != "" || got.Model != "" || got.BaseDirectory != "" {
		t.Fatalf("skill is not inline-only: %+v", got)
	}
	if _, getErr := backend.Get(context.Background(), "missing"); getErr == nil {
		t.Fatal("Get(missing) succeeded")
	}
	metadata[0].Name = "changed"
	again, _ := backend.List(context.Background())
	if again[0].Name != automationSkillName {
		t.Fatal("List exposed mutable catalog metadata")
	}
}

func TestEmbeddedSkillsRejectInvalidContent(t *testing.T) {
	t.Parallel()
	valid := "---\nname: hearth-automations\ndescription: Automation guidance\n---\nInstructions\n"
	tests := map[string]string{
		"missing opening":        strings.TrimPrefix(valid, "---\n"),
		"missing closing":        strings.Replace(valid, "\n---\n", "\n", 1),
		"malformed yaml":         strings.Replace(valid, "description: Automation guidance", "description: [", 1),
		"unknown field":          strings.Replace(valid, "description:", "unknown: value\ndescription:", 1),
		"duplicate field":        strings.Replace(valid, "description:", "name: hearth-automations\ndescription:", 1),
		"non-string description": strings.Replace(valid, "Automation guidance", "123", 1),
		"wrong name":             strings.Replace(valid, automationSkillName, "other", 1),
		"empty name":             strings.Replace(valid, automationSkillName, "", 1),
		"empty description":      strings.Replace(valid, "Automation guidance", "'  '", 1),
		"empty body":             strings.Replace(valid, "Instructions", "  ", 1),
		"fork context":           strings.Replace(valid, "description:", "context: fork\ndescription:", 1),
		"agent override":         strings.Replace(valid, "description:", "agent: other\ndescription:", 1),
		"model override":         strings.Replace(valid, "description:", "model: other\ndescription:", 1),
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := newEmbeddedSkillsFromContent(content); err == nil {
				t.Fatal("invalid content accepted")
			}
		})
	}
}

func TestEmbeddedAutomationExampleDecodes(t *testing.T) {
	t.Parallel()
	backend, err := newEmbeddedSkills()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := backend.Get(context.Background(), automationSkillName)
	if err != nil {
		t.Fatal(err)
	}
	_, example, found := strings.Cut(loaded.Content, "```json\n")
	if !found {
		t.Fatal("missing JSON example")
	}
	example, _, found = strings.Cut(example, "\n```")
	if !found {
		t.Fatal("unterminated JSON example")
	}
	for _, placeholder := range []string{"<BUTTON_ENTITY_ID>", "<FAN_POWER_ENTITY_ID>", "<LAMP_1_POWER_ENTITY_ID>", "<LAMP_2_POWER_ENTITY_ID>"} {
		example = strings.ReplaceAll(example, placeholder, "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	}
	var input struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err = json.Unmarshal([]byte(example), &input); err != nil {
		t.Fatal(err)
	}
	if _, err = automations.DecodeDefinition(input.Definition); err != nil {
		t.Fatalf("example definition does not decode: %v", err)
	}
}
