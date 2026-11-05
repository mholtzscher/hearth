package mcpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

//nolint:gochecknoglobals // Only the test below owns this schema hook's changing document.
var changingOutputDocument json.RawMessage

type changingOutput struct {
	Value string `json:"value"`
}

func (changingOutput) Schema(huma.Registry) *huma.Schema {
	return fixtureSchema(changingOutputDocument)
}

func TestRegistrationsSnapshotChangedOutputSchemaHooks(t *testing.T) {
	t.Parallel()
	t.Cleanup(func() { changingOutputDocument = nil })
	register := func(document string) *mcpapi.Server {
		changingOutputDocument = json.RawMessage(document)
		server := mcpapi.New(mcpapi.Config{Name: "changing-output", Version: "1"})
		mcpapi.Register(server, mcpapi.Tool[greetInput, changingOutput]{
			Name: "changing",
			Handler: func(_ context.Context, input greetInput) (changingOutput, error) {
				return changingOutput{Value: input.Name}, nil
			},
		})
		return server
	}
	const alpha = `{"type":"object","properties":{"value":{"const":"alpha"}},"required":["value"]}`
	first := register(alpha)
	second := register(`{"type":"object","properties":{"value":{"const":"beta"}},"required":["value"]}`)
	third := register(alpha)
	for _, test := range []struct {
		server *mcpapi.Server
		value  string
		valid  bool
	}{
		{first, "alpha", true}, {first, "beta", false},
		{second, "beta", true}, {second, "alpha", false},
		{third, "alpha", true}, {third, "beta", false},
	} {
		result := callExactWireTool(t, test.server.HTTPHandler(), "changing", `{"name":"`+test.value+`"}`)
		if !test.valid {
			if !result.IsError ||
				string(result.Structured) != `{"failure_code":"internal_error","message":"internal error"}` {
				t.Fatalf("wrong output accepted or leaked: %+v", result)
			}
			continue
		}
		want := `{"value":"` + test.value + `"}`
		if result.IsError || string(result.Structured) != want || result.Content[0].Text != want {
			t.Fatalf("schema snapshot changed: %+v, want %s", result, want)
		}
	}
	// A prior successful registration of this Go type must not suppress a later
	// invalid contract's startup failure, including blocked external references.
	changingOutputDocument = json.RawMessage(`{"type":"object","$ref":"file:///forbidden.schema.json"}`)
	assertRegistrationPanics[greetInput, changingOutput](t)
}
