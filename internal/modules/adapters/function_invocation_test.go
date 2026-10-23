package adapters_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestPrepareInvocationOwnsValidatedDescriptorAndArguments(t *testing.T) {
	t.Parallel()
	const revision = adapters.CatalogID("afc_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	function := adapters.Function{AdapterID: "bridge", CatalogRevision: revision, Descriptor: adapters.Descriptor{
		Key:         "inspect",
		Description: "Inspect without changing state",
		TimeoutMS:   1000,
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"n":{"const":9007199254740993}},"required":["n"],"additionalProperties":false}`,
		),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	input := adapters.InvokeInput{AdapterID: "bridge", FunctionKey: "inspect", CatalogRevision: revision,
		Arguments: json.RawMessage(` {"n":9007199254740993} `)}
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	const id = adapters.InvocationID("afi_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	prepared, err := adapters.PrepareInvocation(function, input, id, start)
	if err != nil {
		t.Fatal(err)
	}
	input.Arguments[0] = '['
	function.Descriptor.InputSchema[0] = '['
	copyFunction := prepared.Function()
	copyFunction.Descriptor.OutputSchema[0] = '['
	copyArguments := prepared.Arguments()
	copyArguments[0] = '['
	if !prepared.Ready() || prepared.ID() != id || string(prepared.Arguments()) != `{"n":9007199254740993}` ||
		string(prepared.Function().Descriptor.OutputSchema) != `{"type":"object"}` ||
		!prepared.StartedAt().Equal(start) || !prepared.Deadline().Equal(start.Add(time.Second)) {
		t.Fatal("preparation did not retain an immutable, precise, deadline-scoped value")
	}
	if (adapters.PreparedInvocation{}).Ready() {
		t.Fatal("zero value is admission-ready")
	}
}

func TestPrepareInvocationRejectsFreelyConstructedInvalidInput(t *testing.T) {
	t.Parallel()
	const revision = adapters.CatalogID("afc_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	for name, change := range map[string]func(*adapters.Function, *adapters.InvokeInput, *time.Time){
		"route scope": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) { input.AdapterID = "other" },
		"key scope":   func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) { input.FunctionKey = "other" },
		"invalid id": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) {
			input.CatalogRevision = "afc_invalid"
		},
		"missing args": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) { input.Arguments = nil },
		"null args": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) {
			input.Arguments = json.RawMessage(`null`)
		},
		"duplicate args": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) {
			input.Arguments = json.RawMessage(`{"x":1,"x":2}`)
		},
		"schema violation": func(_ *adapters.Function, input *adapters.InvokeInput, _ *time.Time) {
			input.Arguments = json.RawMessage(`{"x":1}`)
		},
		"unsafe schema": func(function *adapters.Function, _ *adapters.InvokeInput, _ *time.Time) {
			function.Descriptor.InputSchema = json.RawMessage(`{"type":"object","$ref":"file:///example"}`)
		},
		"blank description": func(function *adapters.Function, _ *adapters.InvokeInput, _ *time.Time) {
			function.Descriptor.Description = " "
		},
		"missing timeout": func(function *adapters.Function, _ *adapters.InvokeInput, _ *time.Time) {
			function.Descriptor.TimeoutMS = 0
		},
		"missing time": func(_ *adapters.Function, _ *adapters.InvokeInput, start *time.Time) { *start = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			function := adapters.Function{
				AdapterID:       "bridge",
				CatalogRevision: revision,
				Descriptor: adapters.Descriptor{
					Key:          "inspect",
					Description:  "Inspect",
					TimeoutMS:    1000,
					InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
					OutputSchema: json.RawMessage(`{"type":"object"}`),
				},
			}
			input := adapters.InvokeInput{
				AdapterID:       "bridge",
				FunctionKey:     "inspect",
				CatalogRevision: revision,
				Arguments:       json.RawMessage(`{}`),
			}
			start := time.Now().UTC()
			change(&function, &input, &start)
			if _, err := adapters.PrepareInvocation(
				function,
				input,
				"afi_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				start,
			); !errors.Is(
				err,
				adapters.ErrInvalidFunctionInput,
			) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPrepareInvocationRejectsInvalidReservedIdentity(t *testing.T) {
	t.Parallel()
	function := adapters.Function{
		AdapterID:       "bridge",
		CatalogRevision: "afc_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Descriptor:      contractDescriptor(),
	}
	input := adapters.InvokeInput{AdapterID: function.AdapterID, FunctionKey: function.Descriptor.Key,
		CatalogRevision: function.CatalogRevision, Arguments: json.RawMessage(`{}`)}
	for _, id := range []adapters.InvocationID{"", "afi_invalid", "afc_01890f47-7a6b-7c4d-8e9f-0123456789ab", "afi_01890f47-7a6b-4c4d-8e9f-0123456789ab"} {
		if _, err := adapters.PrepareInvocation(
			function,
			input,
			id,
			time.Now().UTC(),
		); !errors.Is(
			err,
			adapters.ErrInvalidFunctionInput,
		) {
			t.Fatalf("id=%s err=%v", id, err)
		}
	}
}
