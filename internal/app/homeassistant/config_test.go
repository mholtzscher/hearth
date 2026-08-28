package homeassistant_test

import (
	"path/filepath"
	"testing"

	apphomeassistant "github.com/mholtzscher/hearth/internal/app/homeassistant"
)

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := apphomeassistant.LoadConfig(filepath.Join("..", "..", "..", "configs", "homeassistant.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.Binding.EntityExternalID != "light.office" {
		t.Fatalf("entity_id = %q", value.Binding.EntityExternalID)
	}
}
