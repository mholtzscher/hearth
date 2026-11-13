package lockv1_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/lockv1"
)

func TestLockSchemaRejectsNullAndInvalidSupport(t *testing.T) {
	t.Parallel()
	codecs, err := lockv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, decodeErr := codecs.State.Decode(json.RawMessage(`null`)); decodeErr == nil {
		t.Error("null State was accepted")
	}
	for _, raw := range []string{
		`{}`,
		`{"state":{},"operations":null}`,
		`{"state":{"extra":true},"operations":{}}`,
		`{"state":{},"operations":{"set":null}}`,
		`{"state":{},"operations":{"set":{"extra":true}}}`,
		`{"state":{},"operations":{"other":{}}}`,
		`{"state":{},"operations":{},"extra":true}`,
	} {
		if _, _, decodeErr := codecs.Support.Decode(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("invalid support %s was accepted", raw)
		}
	}
}
