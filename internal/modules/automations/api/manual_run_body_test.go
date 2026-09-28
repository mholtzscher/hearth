package api_test

import (
	"strings"
	"testing"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
)

// StartAutomationRunBody.UnmarshalJSON is the strict decoder Huma cannot fully
// supply. It must accept only exactly one JSON object with one optional literal
// boolean member, and it must never echo the payload in its error text.
func TestStartAutomationRunBodyUnmarshalIsStrict(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		body  string
		want  bool
		valid bool
	}{
		{name: "empty object applies", body: `{}`, want: false, valid: true},
		{name: "explicit true", body: `{"bypass_conditions":true}`, want: true, valid: true},
		{name: "explicit false", body: `{"bypass_conditions":false}`, want: false, valid: true},
		{name: "whitespace is tolerated", body: "{ \"bypass_conditions\" :  true }", want: true, valid: true},
		{name: "null body", body: `null`, valid: false},
		{name: "null bypass", body: `{"bypass_conditions":null}`, valid: false},
		{name: "number bypass", body: `{"bypass_conditions":1}`, valid: false},
		{name: "string bypass", body: `{"bypass_conditions":"true"}`, valid: false},
		{name: "capitalized bypass", body: `{"bypass_conditions":True}`, valid: false},
		{name: "unknown member", body: `{"other":false}`, valid: false},
		{name: "array body", body: `[]`, valid: false},
		{name: "scalar body", body: `false`, valid: false},
		{name: "trailing content", body: `{} {}`, valid: false},
	} {
		var body automationsapi.StartAutomationRunBody
		err := body.UnmarshalJSON([]byte(test.body))
		if test.valid {
			if err != nil {
				t.Fatalf("%s: unmarshal = %v, want success", test.name, err)
			}
			if body.BypassConditions != test.want {
				t.Fatalf("%s: bypass = %v, want %v", test.name, body.BypassConditions, test.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: unmarshal succeeded, want a fixed rejection", test.name)
		}
		if strings.ContainsAny(err.Error(), "{}\"[]`") {
			t.Fatalf("%s: error echoed the payload: %v", test.name, err)
		}
	}
}
