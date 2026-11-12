package esphome //nolint:testpackage // Fixtures feed unexported planning and runtime seams.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

// deviceFixture is one source-derived entity list; see testdata/README.md.
type deviceFixture struct {
	Info     nativeapi.DeviceInfo    `json:"info"`
	Entities []nativeapi.EntityInfo  `json:"entities"`
	States   []nativeapi.StateUpdate `json:"states"`
}

func loadFixture(t *testing.T, name string) deviceFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var fixture deviceFixture
	if unmarshalErr := json.Unmarshal(
		raw,
		&fixture, //nolint:musttag // Fixture uses the API structs' default JSON names.
	); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	return fixture
}

func fixtureMAC(t *testing.T, fixture deviceFixture) MAC {
	t.Helper()
	mac, err := ParseMAC(fixture.Info.MAC)
	if err != nil {
		t.Fatal(err)
	}
	return mac
}

func mustPlan(t *testing.T, fixture deviceFixture, exposure Exposure) devicePlan {
	t.Helper()
	plan, err := planDevice(fixtureMAC(t, fixture), fixture.Info, fixture.Entities, exposure)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func refs(t *testing.T, texts ...string) map[EntityRef]struct{} {
	t.Helper()
	set := map[EntityRef]struct{}{}
	for _, text := range texts {
		ref, err := ParseEntityRef(text)
		if err != nil {
			t.Fatal(err)
		}
		set[ref] = struct{}{}
	}
	return set
}
