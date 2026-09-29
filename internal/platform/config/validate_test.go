package config_test

import (
	"testing"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

func TestValidators(t *testing.T) {
	t.Parallel()
	if err := platformconfig.ValidateSlug("adapter_id", "homeassistant-migration"); err != nil {
		t.Fatal(err)
	}
	if err := platformconfig.ValidateNATSURL("nats://127.0.0.1:4222"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"HomeAssistant", "contains.period", ""} {
		if err := platformconfig.ValidateSlug("adapter_id", value); err == nil {
			t.Fatalf("slug %q unexpectedly accepted", value)
		}
	}
	if err := platformconfig.ValidateNATSURL("http://127.0.0.1:4222"); err == nil {
		t.Fatal("non-NATS URL unexpectedly accepted")
	}
}
