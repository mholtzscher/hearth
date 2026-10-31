package mcpapi //nolint:testpackage // Exercise the operation's narrow service double.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestInvokePassesExtremeNumbersExactlyOnce(t *testing.T) {
	t.Parallel()
	const arguments = `{"huge":1e10000,"tiny":1e-10000,"near":1.0000000000000001,"large":9007199254740993,"nested":{"value":null}}`
	calls := 0
	functions := &stubFunctions{invoke: func(
		_ context.Context,
		input adapters.InvokeInput,
	) (adapters.Invocation, error) {
		calls++
		if string(input.Arguments) != arguments {
			t.Fatalf("arguments = %s, want %s", input.Arguments, arguments)
		}
		return functionFixture(adapters.Succeeded{Result: json.RawMessage(`{}`)}), nil
	}}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	registerInvokeAdapterFunction(server, &handler{functions: functions})
	request := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision +
		`","arguments":` + arguments + `}`
	result := functionMCPWire(t, server, request)
	var receipt struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &receipt); err != nil || receipt.Status != "succeeded" || calls != 1 {
		t.Fatalf("result=%s calls=%d err=%v", result, calls, err)
	}
}

func TestInvokeTypeMismatchIdentifiesArguments(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	registerInvokeAdapterFunction(server, &handler{functions: &stubFunctions{}})
	request := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","arguments":"PRIVATE-value"}`
	result := functionMCPWire(t, server, request)
	var failure struct {
		Code        string `json:"failure_code"`
		Diagnostics []struct {
			Code string `json:"code"`
			Path string `json:"path"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(result, &failure); err != nil || failure.Code != "invalid_arguments" {
		t.Fatalf("unexpected failure=%s err=%v", result, err)
	}
	if len(failure.Diagnostics) != 1 || failure.Diagnostics[0].Code != "type_mismatch" ||
		failure.Diagnostics[0].Path != "/arguments" {
		t.Fatalf("diagnostics=%+v, want type_mismatch at /arguments", failure.Diagnostics)
	}
}

func TestFunctionPageLimitExactIntegerSpellings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{"1.0", true}, {"1e0", true}, {"1.0000000000000001", false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			calls := 0
			functions := &stubFunctions{list: func(
				_ context.Context, _ string, page adapters.PageInput,
			) (adapters.FunctionPage, error) {
				calls++
				if page.Limit != 1 {
					t.Fatalf("limit = %d, want 1", page.Limit)
				}
				return adapters.FunctionPage{}, nil
			}}
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			registerListAdapterFunctions(server, &handler{functions: functions})
			request := `{"adapter_id":"demo","limit":` + test.raw + `}`
			result := functionToolWire(t, server, "list_adapter_functions", request)
			var fields map[string]any
			if err := json.Unmarshal(result, &fields); err != nil {
				t.Fatal(err)
			}
			if test.want && (calls != 1 || fields["failure_code"] != nil) ||
				!test.want && (calls != 0 || fields["failure_code"] != "invalid_limit") {
				t.Fatalf("result = %s, calls = %d", result, calls)
			}
		})
	}
}
