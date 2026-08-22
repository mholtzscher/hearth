package entitytypes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEntityTypeManifestsMatchAuthoritativeSchema(t *testing.T) {
	schema, err := os.ReadFile("entitytype-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	codec, err := CompileJSONCodec[map[string]any](
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
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := codec.Decode(json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
