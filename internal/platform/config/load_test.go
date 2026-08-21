package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileIsStrict(t *testing.T) {
	type testConfig struct {
		Name string `yaml:"name"`
	}
	write := func(t *testing.T, contents string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	var valid testConfig
	if err := LoadFile(write(t, "name: hearth\n"), &valid); err != nil {
		t.Fatal(err)
	}
	if valid.Name != "hearth" {
		t.Fatalf("name = %q", valid.Name)
	}

	for _, contents := range []string{
		"name: hearth\nunknown: true\n",
		"name: hearth\n---\nname: second\n",
	} {
		var value testConfig
		if err := LoadFile(write(t, contents), &value); err == nil {
			t.Fatalf("invalid config unexpectedly accepted: %s", strings.TrimSpace(contents))
		}
	}
}

func TestValidators(t *testing.T) {
	if err := ValidateSlug("adapter_id", "homeassistant-migration"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateNATSURL("nats://127.0.0.1:4222"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"HomeAssistant", "contains.period", ""} {
		if err := ValidateSlug("adapter_id", value); err == nil {
			t.Fatalf("slug %q unexpectedly accepted", value)
		}
	}
	if err := ValidateNATSURL("http://127.0.0.1:4222"); err == nil {
		t.Fatal("non-NATS URL unexpectedly accepted")
	}
}
