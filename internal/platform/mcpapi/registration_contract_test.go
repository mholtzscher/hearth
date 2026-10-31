package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type outputCase[O any] struct {
	name  string
	value O
	want  string // Empty means a generic internal failure.
}

func assertOutputCases[O any](t *testing.T, cases []outputCase[O]) {
	t.Helper()
	server := mcpapi.New(mcpapi.Config{Name: "outputs", Version: "1"})
	for index, test := range cases {
		name := fmt.Sprintf("case_%d", index)
		mcpapi.Register(server, mcpapi.Tool[noInput, O]{Name: name,
			Handler: func(context.Context, noInput) (O, error) { return test.value, nil },
		})
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := callExactWireTool(t, server.HTTPHandler(), name, `{}`)
			if test.want == "" {
				const want = `{"failure_code":"internal_error","message":"internal error"}`
				if !result.IsError || string(result.Structured) != want || result.Content[0].Text != want {
					t.Fatalf("invalid success leaked: %+v", result)
				}
			} else if result.IsError || string(result.Structured) != test.want || len(result.Content) != 1 || result.Content[0].Text != test.want {
				t.Fatalf("success = %+v, want %s", result, test.want)
			}
		})
	}
}

func assertContractOutputs[S contractFixture](t *testing.T, cases []outputCase[any]) {
	t.Helper()
	wrapped := make([]outputCase[contractOutput[S]], 0, len(cases))
	for _, test := range cases {
		wrapped = append(wrapped, outputCase[contractOutput[S]]{test.name, contractOutput[S]{test.value}, test.want})
	}
	assertOutputCases(t, wrapped)
}

func TestOutputsValidateActualJSON(t *testing.T) {
	t.Parallel()
	t.Run("pointer element", func(t *testing.T) {
		t.Parallel()
		assertOutputCases(t, []outputCase[*exactOutput]{
			{"valid", &exactOutput{Value: 9007199254740993}, `{"value":9007199254740993}`}, {"nil pointer", nil, ""},
		})
	})
	t.Run("object map and defaults", func(t *testing.T) {
		t.Parallel()
		assertContractOutputs[requiredDefaultContract](t, []outputCase[any]{
			{"valid", map[string]any{"value": 8}, `{"value":8}`},
			{"nil map", (map[string]any)(nil), ""}, {"required default is not inserted", map[string]any{}, ""},
			{"failure shaped success", map[string]any{"failure_code": "refused", "message": "refused"}, ""},
		})
	})
	t.Run("custom marshaler", func(t *testing.T) {
		t.Parallel()
		assertOutputCases(t, []outputCase[customOutput]{
			{"valid", customOutput{Value: 3}, `{"value":3}`},
			{"invalid marshaled property", customOutput{Value: -1}, ""},
		})
	})
	t.Run("optional output defaults remain absent", func(t *testing.T) {
		t.Parallel()
		assertContractOutputs[optionalDefaultContract](
			t,
			[]outputCase[any]{{"no default injection", map[string]any{}, `{}`}},
		)
	})
	t.Run("object root with draft7 reference", func(t *testing.T) {
		t.Parallel()
		assertContractOutputs[nullableReferenceContract](t, []outputCase[any]{
			{"object control", json.RawMessage(`{}`), `{}`}, {"null rejected", nil, ""},
		})
	})
	t.Run("opaque numeric literals", func(t *testing.T) {
		t.Parallel()
		const want = `{"integer":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null}`
		assertContractOutputs[openObjectContract](t, []outputCase[any]{{"exact success", json.RawMessage(want), want}})
	})
	t.Run("recursive", func(t *testing.T) {
		t.Parallel()
		assertRecursiveOutputs[recursiveContract](t)
	})
	t.Run("recursive with id", func(t *testing.T) {
		t.Parallel()
		assertRecursiveOutputs[identifiedRecursiveContract](t)
	})
}

func assertRecursiveOutputs[S contractFixture](t *testing.T) {
	t.Helper()
	const valid = `{"value":9007199254740993,"tag":"leaf","children":[{"value":null}]}`
	assertContractOutputs[S](t, []outputCase[any]{
		{"valid exact recursive", json.RawMessage(valid), valid},
		{"invalid recursive child", json.RawMessage(`{"value":1,"children":[{"value":"bad"}]}`), ""},
		{"invalid embedded relative ref", json.RawMessage(`{"value":1,"tag":"wrong"}`), ""},
		{"failure shaped success", json.RawMessage(`{"failure_code":"refused","message":"refused"}`), ""},
		{"nil", nil, ""},
	})
}

type customOutput struct {
	Value int `json:"value"`
}

func (output customOutput) MarshalJSON() ([]byte, error) {
	if output.Value < 0 {
		return []byte(`{"value":"PRIVATE-invalid"}`), nil
	}
	return fmt.Appendf(nil, `{"value":%d}`, output.Value), nil
}

type countedOutput struct{ count *atomic.Int64 }

func (output countedOutput) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"value":%d}`, output.count.Add(1)), nil
}

func (countedOutput) Schema(huma.Registry) *huma.Schema {
	return fixtureSchema(json.RawMessage(`{"type":"object","properties":{"value":{"const":1}},"required":["value"]}`))
}

func TestSuccessEncodesTheHandlerResultOnce(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	server := mcpapi.New(mcpapi.Config{Name: "one-encoding", Version: "1"})
	mcpapi.Register(server, mcpapi.Tool[noInput, countedOutput]{
		Name:    "counted",
		Handler: func(context.Context, noInput) (countedOutput, error) { return countedOutput{count: &count}, nil },
	})
	result := callExactWireTool(t, server.HTTPHandler(), "counted", `{}`)
	if result.IsError || string(result.Structured) != `{"value":1}` || result.Content[0].Text != `{"value":1}` ||
		count.Load() != 1 {
		t.Fatalf("handler result encoded more than once: %+v, count=%d", result, count.Load())
	}
}

func assertRegistrationPanics[I, O any](t *testing.T) {
	t.Helper()
	for range 2 {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("registered invalid contracts")
				}
			}()
			mcpapi.Register(mcpapi.New(mcpapi.Config{Name: "invalid", Version: "1"}), mcpapi.Tool[I, O]{
				Name: "invalid", Handler: func(context.Context, I) (O, error) { var zero O; return zero, nil },
			})
		}()
	}
}

func TestInvalidToolSchemasFailStartup(t *testing.T) {
	t.Parallel()
	assertRegistrationPanics[noInput, any](t)
	assertRegistrationPanics[noInput, int](t)
	assertRegistrationPanics[noInput, []string](t)
	assertRegistrationPanics[int, exactOutput](t)
	assertRegistrationPanics[noInput, contractOutput[nonObjectReferenceContract]](t)
	assertRegistrationPanics[contractInput[nonObjectReferenceContract], exactOutput](t)
	assertRegistrationPanics[noInput, chan int](t)
	assertRegistrationPanics[chan int, exactOutput](t)
	assertRegistrationPanics[contractInput[missingReferenceContract], exactOutput](t)
	assertRegistrationPanics[contractInput[malformedContract], exactOutput](t)
	assertRegistrationPanics[contractInput[unmarshalableContract], exactOutput](t)
	assertRegistrationPanics[noInput, contractOutput[missingReferenceContract]](t)
	assertRegistrationPanics[noInput, contractOutput[malformedContract]](t)
	assertRegistrationPanics[noInput, contractOutput[unmarshalableContract]](t)
}

func TestPreparationNeverLoadsExternalResources(t *testing.T) {
	t.Parallel()
	assertRegistrationPanics[noInput, contractOutput[externalReferenceContract]](t)
	assertRegistrationPanics[noInput, contractOutput[fileReferenceContract]](t)
	assertRegistrationPanics[contractInput[externalReferenceContract], exactOutput](t)
	assertRegistrationPanics[contractInput[fileReferenceContract], exactOutput](t)
}

func TestFailuresRetainModeledAndUnexpectedPolicies(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "failures", Version: "1"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, exactOutput]{Name: "failure",
		Handler: func(_ context.Context, input greetInput) (exactOutput, error) {
			if input.Name == "unknown" {
				return exactOutput{}, errors.New("PRIVATE-service-cause")
			}
			return exactOutput{}, &mcpapi.ToolError{Code: "refused", Message: "refused", Details: map[string]any{
				"evidence": json.RawMessage(
					`{"integer":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null}`,
				),
			}}
		},
	})
	result := callExactWireTool(t, server.HTTPHandler(), "failure", `{"name":"modeled"}`)
	if !result.IsError || result.Content[0].Text != string(result.Structured) {
		t.Fatalf("modeled failure text differs: %+v", result)
	}
	for _, token := range []string{`9007199254740993`, `1e-10000`, `1e10000`} {
		if !strings.Contains(string(result.Structured), token) {
			t.Fatalf("modeled failure lost exact details: %+v", result)
		}
	}
	unknown := callExactWireTool(t, server.HTTPHandler(), "failure", `{"name":"unknown"}`)
	if !unknown.IsError || unknown.Content[0].Text != "internal error" || len(unknown.Structured) != 0 {
		t.Fatalf("private cause escaped: %+v", unknown)
	}
}
