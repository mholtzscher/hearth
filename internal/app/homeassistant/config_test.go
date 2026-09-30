package homeassistant_test

import (
	"path/filepath"
	"testing"

	apphomeassistant "github.com/mholtzscher/hearth/internal/app/homeassistant"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := loadConfig(filepath.Join("..", "..", "..", "configs", "homeassistant.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.Binding.EntityExternalID != "light.office" {
		t.Fatalf("entity_id = %q", value.Binding.EntityExternalID)
	}
}

func loadConfig(path string) (apphomeassistant.Config, error) {
	value, err := platformconfig.LoadYAML[apphomeassistant.Config](path, true)
	if err != nil {
		return apphomeassistant.Config{}, err
	}
	if validationErr := value.Validate(); validationErr != nil {
		return apphomeassistant.Config{}, platformconfig.Invalid(path, validationErr)
	}
	return value, nil
}
