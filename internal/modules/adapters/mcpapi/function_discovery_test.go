package mcpapi //nolint:testpackage // Independent MCP wire fixtures and selected result contracts.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestFunctionDiscoverySummariesAndExactSchemaDetail(t *testing.T) {
	t.Parallel()
	descriptor := functionFixture(nil).Function
	descriptor.InputSchema = json.RawMessage(
		`{"type":"object","properties":{"n":{"const":9007199254740993},"decimal":{"const":1.0000000000000001},"nullable":{"const":null}}}`,
	)
	function := adapters.Function{
		AdapterID:       "demo",
		CatalogRevision: functionRevision,
		Descriptor:      descriptor,
		Callable:        false,
	}
	stub := &stubFunctions{
		list: func(_ context.Context, id string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if id != "demo" || page.Limit != 2 || page.Cursor != "opaque-scoped-cursor" {
				t.Fatalf("list=%s %+v", id, page)
			}
			return adapters.FunctionPage{Items: []adapters.Function{function}, NextCursor: "next-scoped-cursor"}, nil
		},
		get: func(_ context.Context, id string, key adapters.FunctionKey) (adapters.Function, error) {
			if id != "demo" || key != "inspect" {
				t.Fatalf("get=%s %s", id, key)
			}
			return function, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, nil, stub)
	summary := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","description":"Inspect once","callable":false,"timeout_ms":1000}`
	page := functionToolWire(
		t,
		server,
		"list_adapter_functions",
		`{"adapter_id":"demo","limit":2,"cursor":"opaque-scoped-cursor"}`,
	)
	wantPage := `{"items":[` + summary + `],"next_cursor":"next-scoped-cursor"}`
	if string(page) != wantPage {
		t.Fatalf("summary=%s, want=%s", page, wantPage)
	}
	detail := functionToolWire(t, server, "get_adapter_function", `{"adapter_id":"demo","function_key":"inspect"}`)
	wantDetail := strings.TrimSuffix(
		summary,
		"}",
	) + `,"input_schema":` + string(
		descriptor.InputSchema,
	) + `,"output_schema":{"type":"object"}}`
	if string(detail) != wantDetail {
		t.Fatalf("detail=%s, want=%s", detail, wantDetail)
	}
	functionSchemaAccepts(t, compileFunctionSchema(t, functionSummaryPageSchema()), string(page), true)
	functionSchemaAccepts(t, compileFunctionSchema(t, functionDetailSchema()), string(detail), true)
	functionSchemaAccepts(
		t,
		compileFunctionSchema(t, functionDetailSchema()),
		strings.Replace(string(detail), `"type":"object"`, `"type":"array"`, 1),
		false,
	)
	functionSchemaAccepts(
		t,
		compileFunctionSchema(t, functionDetailSchema()),
		strings.Replace(string(detail), `"const":9007199254740993`, `"$ref":"https://private.invalid"`, 1),
		false,
	)
}

func TestFunctionHistoryRunningNullScopeAndSummaryPrivacy(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		history: func(_ context.Context, in adapters.InvocationFilter) (adapters.InvocationPage, error) {
			if in.AdapterID != "demo" || in.FunctionKey != "removed" || in.Limit != 200 ||
				in.Cursor != "history-cursor" {
				t.Fatalf("filter=%+v", in)
			}
			return adapters.InvocationPage{Items: []adapters.Invocation{functionFixture(nil)}}, nil
		},
		detail: func(_ context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
			if id != functionInvocationID {
				t.Fatalf("id=%s", id)
			}
			return functionFixture(nil), nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, nil, stub)
	page := functionToolWire(
		t,
		server,
		"list_adapter_function_invocations",
		`{"adapter_id":"demo","function_key":"removed","limit":200,"cursor":"history-cursor"}`,
	)
	summary := `{"invocation_id":"` + functionInvocationID + `","adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","status":"running","started_at":"2026-10-18T01:02:03.000000456Z","completed_at":null}`
	if string(page) != `{"items":[`+summary+`]}` {
		t.Fatalf("running summary=%s", page)
	}
	detail := functionToolWire(
		t,
		server,
		"get_adapter_function_invocation",
		`{"invocation_id":"`+functionInvocationID+`"}`,
	)
	want := strings.TrimSuffix(
		summary,
		"}",
	) + `,"arguments":` + functionArguments + `,"deadline":"2026-10-18T01:02:04.000000456Z"}`
	if string(detail) != want {
		t.Fatalf("running detail=%s, want=%s", detail, want)
	}
	schema := compileFunctionSchema(t, invocationDetailSchema())
	functionSchemaAccepts(t, schema, string(detail), true)
	functionSchemaAccepts(
		t,
		schema,
		strings.Replace(string(detail), `"completed_at":null`, `"completed_at":"2026-10-18T01:02:03Z"`, 1),
		false,
	)
	functionSchemaAccepts(t, schema, strings.TrimSuffix(string(detail), "}")+`,"result":{}}`, false)
	functionSchemaAccepts(t, schema, strings.TrimSuffix(string(detail), "}")+`,"reason_code":"unknown"}`, false)
}

func TestFunctionEmptyCollectionsAndDefaults(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		list: func(_ context.Context, id string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if id != "demo" || page.Limit != 50 || page.Cursor != "" {
				t.Fatalf("list=%s %+v", id, page)
			}
			return adapters.FunctionPage{}, nil
		},
		history: func(_ context.Context, in adapters.InvocationFilter) (adapters.InvocationPage, error) {
			if in.AdapterID != "demo" || in.Limit != 50 || in.FunctionKey != "" || in.Cursor != "" {
				t.Fatalf("filter=%+v", in)
			}
			return adapters.InvocationPage{}, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, nil, stub)
	for _, name := range []string{"list_adapter_functions", "list_adapter_function_invocations"} {
		wire := functionToolWire(t, server, name, `{"adapter_id":"demo"}`)
		if string(wire) != `{"items":[]}` {
			t.Fatalf("empty %s=%s", name, wire)
		}
	}
}

func TestFunctionResultContractsRejectContradictoryEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status   string
		evidence adapters.Evidence
	}{
		{"succeeded", adapters.Succeeded{Result: json.RawMessage(functionArguments)}},
		{"failed", adapters.Failed{ReasonCode: "refused"}},
		{"not_executed", adapters.NotExecuted{ReasonCode: "offline"}},
		{"unknown", adapters.Unknown{ReasonCode: "timeout"}},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			item := functionFixture(test.evidence)
			result, err := (&functionHandler{service: &stubFunctions{detail: func(context.Context, adapters.InvocationID) (adapters.Invocation, error) { return item, nil }}}).getInvocation(
				t.Context(),
				invocationInput{InvocationID: functionInvocationID},
			)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			schema := compileFunctionSchema(t, invocationDetailSchema())
			functionSchemaAccepts(t, schema, string(encoded), true)
			contradiction := strings.TrimSuffix(string(encoded), "}") + `,"private":"no"}`
			functionSchemaAccepts(t, schema, contradiction, false)
			if test.status == "succeeded" {
				contradiction = strings.TrimSuffix(string(encoded), "}") + `,"reason_code":"no"}`
			} else {
				contradiction = strings.TrimSuffix(string(encoded), "}") + `,"result":{}}`
			}
			functionSchemaAccepts(t, schema, contradiction, false)
		})
	}
}
