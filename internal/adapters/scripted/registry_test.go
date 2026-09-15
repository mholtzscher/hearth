package scripted_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

func TestRegistryKnowsBuiltinCatalog(t *testing.T) {
	t.Parallel()
	types := scripted.KnownTypes()
	if len(types) != 16 {
		t.Fatalf("KnownTypes() has %d entries, want 16", len(types))
	}
	for _, id := range []string{"hearth.power/v1", "hearth.temperature/v1", "hearth.enumevent/v1"} {
		if _, err := scripted.Lookup(id); err != nil {
			t.Fatalf("Lookup(%q) failed: %v", id, err)
		}
	}
	if _, err := scripted.Lookup("hearth.toaster/v9"); err == nil {
		t.Fatal("Lookup(unknown) succeeded, want an error naming the supported set")
	}
}

func TestRegistryValidatesPowerState(t *testing.T) {
	t.Parallel()
	codecs, err := scripted.Lookup("hearth.power/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, stateErr := codecs.NormalizeState(json.RawMessage(`true`)); stateErr != nil {
		t.Fatalf("valid power State rejected: %v", stateErr)
	}
	if _, stateErr := codecs.NormalizeState(json.RawMessage(`"on"`)); stateErr == nil {
		t.Fatal("invalid power State accepted, want rejection")
	}
	if _, supportErr := codecs.NormalizeSupport(
		json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
	); supportErr != nil {
		t.Fatalf("valid power support rejected: %v", supportErr)
	}
}
