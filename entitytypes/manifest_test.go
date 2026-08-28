package entitytypes_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes"
)

func TestEntityTypeManifestsMatchAuthoritativeSchema(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("entitytype-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	codec, err := entitytypes.CompileJSONCodec[map[string]any](
		"urn:hearth:schema:entity-type-manifest:v1",
		json.RawMessage(schema),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := filepath.Glob(filepath.Join("*", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) < 2 {
		t.Fatalf("found %d Entity-type manifests, want at least two", len(manifests))
	}
	for _, path := range manifests {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			t.Parallel()
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, _, decodeErr := codec.Decode(json.RawMessage(raw)); decodeErr != nil {
				t.Fatal(decodeErr)
			}
		})
	}
}
