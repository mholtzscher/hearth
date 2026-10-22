package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerPrepared[I, O any](server *mcpapi.Server, request bool, tool mcpapi.Tool[I, O]) {
	if !request {
		mcpapi.Register(server, tool)
		return
	}
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[I, O]{
		Name:             tool.Name,
		InputSchema:      tool.InputSchema,
		SuccessSchema:    tool.SuccessSchema,
		ExactFailureText: tool.ExactFailureText,
		Handler:          func(ctx context.Context, _ *mcp.CallToolRequest, input I) (O, error) { return tool.Handler(ctx, input) },
	})
}

type outputCase[O any] struct {
	name  string
	value O
	want  string // Empty means a generic internal failure.
}

func assertPreparedOutputs[O any](t *testing.T, schema any, cases []outputCase[O]) {
	t.Helper()
	for _, request := range []bool{false, true} {
		t.Run(fmt.Sprintf("request=%t", request), func(t *testing.T) {
			t.Parallel()
			server := mcpapi.New(mcpapi.Config{Name: "outputs", Version: "1"})
			for index, test := range cases {
				name := fmt.Sprintf("case_%d", index)
				registerPrepared(server, request, mcpapi.Tool[noInput, O]{Name: name, SuccessSchema: schema,
					Handler: func(context.Context, noInput) (O, error) { return test.value, nil },
				})
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					result := callExactWireTool(t, server.HTTPHandler(), name, `{}`)
					if test.want == "" {
						if !result.IsError ||
							string(
								result.Structured,
							) != `{"failure_code":"internal_error","message":"internal error"}` ||
							result.Content[0].Text != "internal_error: internal error" {
							t.Fatalf("invalid success leaked: %+v", result)
						}
					} else if result.IsError || string(result.Structured) != test.want || len(result.Content) != 1 || result.Content[0].Text != test.want {
						t.Fatalf("success = %+v, want %s", result, test.want)
					}
				})
			}
		})
	}
}

func TestPreparedOutputsValidateActualJSONThroughBothRegistrationPaths(t *testing.T) {
	t.Parallel()
	t.Run("pointer element", func(t *testing.T) {
		t.Parallel()
		assertPreparedOutputs(t, nil, []outputCase[*exactOutput]{
			{"valid", &exactOutput{Value: 9007199254740993}, `{"value":9007199254740993}`}, {"nil pointer", nil, ""},
		})
	})
	t.Run("object map and defaults", func(t *testing.T) {
		t.Parallel()
		schema := json.RawMessage(
			`{"type":"object","properties":{"value":{"type":"integer","default":7}},"required":["value"],"additionalProperties":false}`,
		)
		assertPreparedOutputs(t, schema, []outputCase[map[string]any]{
			{"valid", map[string]any{"value": 8}, `{"value":8}`},
			{"nil map", nil, ""}, {"required default is not inserted", map[string]any{}, ""},
			{"failure shaped success", map[string]any{"failure_code": "refused", "message": "refused"}, ""},
		})
	})
	t.Run("custom marshaler", func(t *testing.T) {
		t.Parallel()
		assertPreparedOutputs(t, nil, []outputCase[customOutput]{
			{
				"valid",
				customOutput{Value: 3},
				`{"value":3}`,
			},
			{"invalid marshaled property", customOutput{Value: -1}, ""},
		})
	})
	t.Run("optional output defaults remain absent", func(t *testing.T) {
		t.Parallel()
		schema := json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer","default":7}}}`)
		assertPreparedOutputs(t, schema, []outputCase[map[string]any]{
			{"no default injection", map[string]any{}, `{}`},
		})
	})
	t.Run("object root with draft7 reference", func(t *testing.T) {
		t.Parallel()
		schema := json.RawMessage(
			`{"$schema":"http://json-schema.org/draft-07/schema#","$ref":"#/definitions/result","definitions":{"result":{"type":["object","null"],"additionalProperties":false}}}`,
		)
		assertPreparedOutputs(t, schema, []outputCase[any]{
			{"object control", json.RawMessage(`{}`), `{}`}, {"null rejected", nil, ""},
		})
	})
	t.Run("opaque numeric literals", func(t *testing.T) {
		t.Parallel()
		const want = `{"integer":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null}`
		assertPreparedOutputs(t, json.RawMessage(`{"type":"object"}`), []outputCase[any]{
			{"exact success", json.RawMessage(want), want},
		})
	})
	for _, id := range []string{"", "https://hearth.test/schemas/tree.json"} {
		t.Run("recursive "+id, func(t *testing.T) {
			t.Parallel()
			schema := recursiveOutputSchema(t)
			if id != "" {
				schema["$id"] = id
			}
			// An explicit nullable schema still has the required object root.
			schema["type"] = []string{"object", "null"}
			schema["$defs"].(map[string]any)["node"].(map[string]any)["type"] = []string{"object", "null"}
			const valid = `{"value":9007199254740993,"tag":"leaf","children":[{"value":null}]}`
			assertPreparedOutputs(t, schema, []outputCase[any]{
				{"valid exact recursive", json.RawMessage(valid), valid},
				{"invalid recursive child", json.RawMessage(`{"value":1,"children":[{"value":"bad"}]}`), ""},
				{"invalid embedded relative ref", json.RawMessage(`{"value":1,"tag":"wrong"}`), ""},
				{"failure shaped success", json.RawMessage(`{"failure_code":"refused","message":"refused"}`), ""},
				{"nil", nil, ""},
			})
		})
	}
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

func TestPreparedSuccessEncodesTheHandlerResultOnce(t *testing.T) {
	t.Parallel()
	for _, request := range []bool{false, true} {
		t.Run(fmt.Sprintf("request=%t", request), func(t *testing.T) {
			t.Parallel()
			var count atomic.Int64
			server := mcpapi.New(mcpapi.Config{Name: "one-encoding", Version: "1"})
			registerPrepared(server, request, mcpapi.Tool[noInput, countedOutput]{
				Name: "counted",
				SuccessSchema: json.RawMessage(
					`{"type":"object","properties":{"value":{"const":1}},"required":["value"]}`,
				),
				Handler: func(context.Context, noInput) (countedOutput, error) { return countedOutput{count: &count}, nil },
			})
			result := callExactWireTool(t, server.HTTPHandler(), "counted", `{}`)
			if result.IsError || string(result.Structured) != `{"value":1}` ||
				result.Content[0].Text != `{"value":1}` ||
				count.Load() != 1 {
				t.Fatalf("handler result encoded more than once: %+v, count=%d", result, count.Load())
			}
		})
	}
}

func assertRegistrationPanics[I, O any](
	t *testing.T,
	input any,
	output any,
) {
	t.Helper()
	for _, request := range []bool{false, true} {
		// Repeated registrations must each reject invalid default contracts.
		for range 2 {
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("request=%t registered invalid contracts", request)
					}
				}()
				registerPrepared(
					mcpapi.New(mcpapi.Config{Name: "invalid", Version: "1"}),
					request,
					mcpapi.Tool[I, O]{
						Name:          "invalid",
						InputSchema:   input,
						SuccessSchema: output,
						Handler:       func(context.Context, I) (O, error) { var zero O; return zero, nil },
					},
				)
			}()
		}
	}
}

func TestInvalidToolSchemasFailStartup(t *testing.T) {
	t.Parallel()
	t.Run("any needs explicit success schema", func(t *testing.T) {
		t.Parallel()
		assertRegistrationPanics[noInput, any](t, nil, nil)
	})
	t.Run("output derivation", func(t *testing.T) {
		t.Parallel()
		assertRegistrationPanics[noInput, chan int](t, nil, nil)
	})
	t.Run("input derivation", func(t *testing.T) {
		t.Parallel()
		assertRegistrationPanics[chan int, exactOutput](t, nil, nil)
	})
	t.Run("SDK owns input reference resolution", func(t *testing.T) {
		t.Parallel()
		input := json.RawMessage(`{"type":"object","$ref":"#/$defs/missing"}`)
		assertRegistrationPanics[noInput, exactOutput](t, input, nil)
	})
	for _, schema := range []any{make(chan int), json.RawMessage(`{`), map[string]any{"$ref": "#/$defs/missing"}} {
		assertRegistrationPanics[noInput, exactOutput](t, nil, schema)
	}
	for _, schema := range []any{make(chan int), json.RawMessage(`{`)} {
		assertRegistrationPanics[noInput, exactOutput](t, schema, nil)
	}
}

func TestOutputPreparationNeverLoadsExternalResources(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "valid-schema.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"file://" + path, "https://hearth.test/missing.json"} {
		func() {
			defer func() {
				failure := recover()
				if failure == nil || !strings.Contains(fmt.Sprint(failure), "external schema loading disabled") {
					t.Fatalf("reference %s: %v", ref, failure)
				}
			}()
			mcpapi.Register(mcpapi.New(mcpapi.Config{Name: "external", Version: "1"}), mcpapi.Tool[noInput, any]{
				Name: "external", SuccessSchema: map[string]any{"$ref": ref},
				Handler: func(context.Context, noInput) (any, error) { return json.RawMessage(`{}`), nil },
			})
		}()
	}
}

func TestPreparedFailuresRetainTheirPolicyThroughBothRegistrationPaths(t *testing.T) {
	t.Parallel()
	for _, request := range []bool{false, true} {
		for _, exact := range []bool{false, true} {
			server := mcpapi.New(mcpapi.Config{Name: "failures", Version: "1"})
			registerPrepared(
				server,
				request,
				mcpapi.Tool[greetInput, exactOutput]{Name: "failure", ExactFailureText: exact,
					Handler: func(_ context.Context, input greetInput) (exactOutput, error) {
						if input.Name == "unknown" {
							return exactOutput{}, errors.New("PRIVATE-service-cause")
						}
						return exactOutput{}, &mcpapi.ToolError{
							Code:    "refused",
							Message: "refused",
							Details: map[string]any{
								"evidence": json.RawMessage(
									`{"integer":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null}`,
								),
							},
						}
					},
				},
			)
			result := callExactWireTool(t, server.HTTPHandler(), "failure", `{"name":"modeled"}`)
			if !result.IsError || !strings.Contains(string(result.Structured), `9007199254740993`) ||
				!strings.Contains(string(result.Structured), `1e-10000`) ||
				!strings.Contains(string(result.Structured), `1e10000`) {
				t.Fatalf("modeled failure lost exact details: %+v", result)
			}
			if exact && result.Content[0].Text != string(result.Structured) {
				t.Fatalf("exact failure = %+v", result)
			}
			if !exact && result.Content[0].Text != "refused: refused" {
				t.Fatalf("prose failure = %+v", result)
			}
			assertUnknownPreparedFailure(t, server)
		}
	}
}

func assertUnknownPreparedFailure(t *testing.T, server *mcpapi.Server) {
	t.Helper()
	unknown := callExactWireTool(t, server.HTTPHandler(), "failure", `{"name":"unknown"}`)
	if !unknown.IsError || unknown.Content[0].Text != "internal error" ||
		strings.Contains(string(unknown.Structured), "PRIVATE") {
		t.Fatalf("private cause escaped: %+v", unknown)
	}
}
