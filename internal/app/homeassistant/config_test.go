package homeassistant

import (
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	value, err := LoadConfig(filepath.Join("..", "..", "..", "configs", "homeassistant.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.Binding.EntityExternalID != "light.office" {
		t.Fatalf("entity_id = %q", value.Binding.EntityExternalID)
	}
}
