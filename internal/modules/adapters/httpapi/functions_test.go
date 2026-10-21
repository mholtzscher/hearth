package httpapi //nolint:testpackage // Public registrations are exercised with shared transport fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionHTTPValidatesOuterBodyAndBoundsBeforeAdmission(t *testing.T) {
	t.Parallel()
	valid := `{"catalog_revision":"` + functionRevision + `","arguments":{}}`
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 422}, {"missing revision", `{"arguments":{}}`, 422},
		{"missing arguments", `{"catalog_revision":"x"}`, 422},
		{"null revision", `{"catalog_revision":null,"arguments":{}}`, 422},
		{"null arguments", `{"catalog_revision":"x","arguments":null}`, 422},
		{"array arguments", `{"catalog_revision":"x","arguments":[]}`, 422},
		{"unknown", `{"catalog_revision":"x","arguments":{},"secret":"never-echo"}`, 422},
		{"duplicate", `{"catalog_revision":"x","arguments":{},"arguments":{}}`, 422},
		{"trailing", valid + `{}`, 422}, {"malformed", `{"secret":"never-echo"`, 422},
		{"oversized malformed", strings.Repeat("!", maximumFunctionBodyBytes+1), 413},
		{"exact bound", valid + strings.Repeat(" ", maximumFunctionBodyBytes-len(valid)), 200},
		{"over bound", valid + strings.Repeat(" ", maximumFunctionBodyBytes-len(valid)+1), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			stub := &stubFunctions{
				invoke: func(_ context.Context, in adapters.InvokeInput) (adapters.Invocation, error) {
					calls++
					if in.AdapterID != "demo" || in.FunctionKey != "inspect" ||
						string(in.CatalogRevision) != functionRevision ||
						string(in.Arguments) != "{}" {
						t.Fatalf("input=%+v", in)
					}
					return functionFixture(adapters.Succeeded{Result: json.RawMessage(`{}`)}), nil
				},
			}
			router, _ := functionHTTP(t, stub)
			response := functionRequest(t, router, http.MethodPost, functionInvokePath, test.body)
			if response.Code != test.status || strings.Contains(response.Body.String(), "never-echo") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			wantCalls := 0
			if test.status == http.StatusOK {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("admissions=%d, want %d", calls, wantCalls)
			}
		})
	}
}

func assertFunctionEvidence(t *testing.T, data []byte, status string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["status"]) != `"`+status+`"` || string(fields["arguments"]) != functionArguments ||
		string(fields["completed_at"]) == "null" {
		t.Fatalf("evidence=%s", data)
	}
	_, resultPresent := fields["result"]
	_, reasonPresent := fields["reason_code"]
	if resultPresent != (status == "succeeded") || reasonPresent != (status != "succeeded") {
		t.Fatalf("contradictory evidence=%s", data)
	}
	if resultPresent && string(fields["result"]) != functionArguments {
		t.Fatalf("rounded result=%s", data)
	}
	for _, private := range []string{"runtime_id", "descriptor", "correlation_id"} {
		if _, present := fields[private]; present {
			t.Fatalf("private %s exposed: %s", private, data)
		}
	}
}
