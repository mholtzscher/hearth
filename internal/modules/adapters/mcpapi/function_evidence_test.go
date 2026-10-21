package mcpapi //nolint:testpackage // Recorder wire tests pin this transport's independent contract.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

//nolint:gocognit // One matrix follows terminal evidence through receipt, detail, and summary.
func TestFunctionTerminalEvidenceAndCompactReceipts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status   string
		reason   string
		evidence adapters.Evidence
	}{
		{"succeeded", "", adapters.Succeeded{Result: json.RawMessage(functionArguments)}},
		{"failed", "upstream_refused", adapters.Failed{ReasonCode: "upstream_refused"}},
		{"not_executed", "adapter_unavailable", adapters.NotExecuted{ReasonCode: "adapter_unavailable"}},
		{"unknown", "outcome_timeout", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			invocation := functionFixture(test.evidence)
			calls := 0
			stub := &stubFunctions{
				invoke: func(_ context.Context, in adapters.InvokeInput) (adapters.Invocation, error) {
					calls++
					if in.AdapterID != "demo" || in.FunctionKey != "inspect" ||
						in.CatalogRevision != functionRevision ||
						string(in.Arguments) != functionArguments {
						t.Fatalf("input=%+v", in)
					}
					return invocation, nil
				},
				detail: func(_ context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
					if id != functionInvocationID {
						t.Fatalf("id=%s", id)
					}
					return invocation, nil
				},
				history: func(_ context.Context, in adapters.InvocationFilter) (adapters.InvocationPage, error) {
					if in.AdapterID != "demo" || in.Limit != 50 {
						t.Fatalf("filter=%+v", in)
					}
					return adapters.InvocationPage{Items: []adapters.Invocation{invocation}}, nil
				},
			}
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			Register(server, nil, stub)
			arguments := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","arguments":` + functionArguments + `}`
			wire := functionMCPWire(t, server, arguments)
			identity := `"invocation_id":"` + functionInvocationID + `","adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","status":"` + test.status + `"`
			if test.status == "succeeded" {
				want := `{` + identity + `,"result":` + functionArguments + `}`
				if string(wire) != want {
					t.Fatalf("receipt=%s, want %s", wire, want)
				}
			} else {
				want := `{"failure_code":"` + test.status + `","message":"function invocation did not succeed","invocation":{` + identity + `,"reason_code":"` + test.reason + `"}}`
				// Key order belongs to the encoder; compare each raw field without
				// turning opaque values into float64.
				assertRawObjectEqual(t, wire, want)
			}
			if calls != 1 {
				t.Fatalf("admissions=%d", calls)
			}
			detail := functionToolWire(
				t,
				server,
				"get_adapter_function_invocation",
				`{"invocation_id":"`+functionInvocationID+`"}`,
			)
			assertInvocationDetail(t, detail, test.status, test.reason)
			page := functionToolWire(t, server, "list_adapter_function_invocations", `{"adapter_id":"demo"}`)
			var decoded struct {
				Items []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(page, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Items) != 1 {
				t.Fatalf("page=%s", page)
			}
			for _, private := range []string{"arguments", "result", "deadline", "runtime", "correlation", "next_cursor"} {
				if strings.Contains(string(page), `"`+private+`"`) {
					t.Fatalf("summary leaked %s: %s", private, page)
				}
			}
			if !strings.Contains(string(page), `"status":"`+test.status+`"`) ||
				!strings.Contains(string(page), `"invocation_id":"`+functionInvocationID+`"`) {
				t.Fatalf("summary=%s", page)
			}
		})
	}
}

func assertInvocationDetail(t *testing.T, data []byte, status, reason string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["invocation_id"]) != `"`+functionInvocationID+`"` || string(fields["status"]) != `"`+status+`"` ||
		string(fields["arguments"]) != functionArguments ||
		string(fields["deadline"]) != `"2026-10-18T01:02:04.000000456Z"` {
		t.Fatalf("detail=%s", data)
	}
	if status == "succeeded" {
		if string(fields["result"]) != functionArguments || fields["reason_code"] != nil {
			t.Fatalf("success=%s", data)
		}
	} else if fields["result"] != nil || string(fields["reason_code"]) != `"`+reason+`"` {
		t.Fatalf("failure=%s", data)
	}
	if string(fields["completed_at"]) != `"2026-10-18T01:02:03.001000456Z"` {
		t.Fatalf("completion=%s", data)
	}
	for _, private := range []string{"id", "runtime_id", "descriptor", "correlation_id"} {
		if fields[private] != nil {
			t.Fatalf("private %s: %s", private, data)
		}
	}
}

func assertRawObjectEqual(t *testing.T, actual []byte, expected string) {
	t.Helper()
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got=%s, want=%s", actual, expected)
	}
	for key, value := range want {
		if string(got[key]) != string(value) {
			t.Fatalf("%s: got=%s, want=%s", key, got[key], value)
		}
	}
}

func TestFunctionFailuresAreSafeAndRetainInvocationIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		code string
	}{
		{adapters.ErrInvalidFunctionInput, "invalid_input"}, {adapters.ErrAdapterNotFound, "adapter_not_found"},
		{adapters.ErrFunctionNotFound, "function_not_found"}, {adapters.ErrInvocationNotFound, "invocation_not_found"},
		{adapters.ErrCatalogChanged, "catalog_changed"}, {adapters.ErrCatalogConflict, "catalog_conflict"},
		{adapters.ErrAdapterUnavailable, "adapter_unavailable"}, {adapters.ErrFunctionAdmissionClosed, "admission_closed"},
		{adapters.ErrFunctionPersistence, "internal_error"}, {errors.New("secret-database-path"), "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: adapters.ErrFunctionPersistence}, "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: errors.Join(adapters.ErrFunctionPersistence, adapters.ErrInvalidFunctionInput)}, "internal_error"},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			t.Parallel()
			calls := 0
			stub := &stubFunctions{invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
				calls++
				return adapters.Invocation{}, fmt.Errorf("secret-validator-value: %w", test.err)
			}}
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			Register(server, nil, stub)
			wire := functionMCPWire(
				t,
				server,
				`{"adapter_id":"demo","function_key":"inspect","catalog_revision":"`+functionRevision+`","arguments":{}}`,
			)
			var known *adapters.InvocationError
			wantID := errors.As(test.err, &known)
			if calls != 1 || !strings.Contains(string(wire), `"failure_code":"`+test.code+`"`) ||
				strings.Contains(string(wire), "secret") ||
				strings.Contains(string(wire), functionInvocationID) != wantID {
				t.Fatalf("calls=%d, failure=%s", calls, wire)
			}
		})
	}
}
