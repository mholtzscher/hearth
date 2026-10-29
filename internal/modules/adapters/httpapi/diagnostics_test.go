package httpapi //nolint:testpackage // Tests include direct Huma StatusError conformance.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestAdapterHTTPDiagnosticsOwnUnexpectedFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, path, body string
		cause                    error
		canceled, wantDiagnostic bool
		status                   int
	}{
		{"management", "GET", "/adapters/synthetic", "", errors.New("SENSITIVE-management"), false, true, 500},
		{"function read", "GET", "/adapters/synthetic/functions/lookup", "", errors.New("SENSITIVE-function"), false, true, 500},
		{"invocation persistence", "POST", "/adapters/synthetic/functions/lookup/invocations", `{"catalog_revision":"afc_01890f47-7a6b-7c4d-8e9f-0123456789ab","arguments":{}}`,
			&adapters.InvocationError{ID: "afi_01890f47-7a6b-7c4d-8e9f-0123456789ab", Err: errors.Join(adapters.ErrFunctionPersistence, errors.New("SENSITIVE-completion"))}, false, true, 500},
		{"caller cancellation", "GET", "/adapters/synthetic", "", context.Canceled, true, false, 500},
		{"dependency deadline", "GET", "/adapters/synthetic", "", context.DeadlineExceeded, false, true, 500},
		{"joined fault", "GET", "/adapters/synthetic", "", errors.Join(context.Canceled, errors.New("SENSITIVE-storage")), true, true, 500},
		{"modeled not found", "GET", "/adapters/synthetic", "", adapters.ErrAdapterNotFound, false, false, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Evidence", "1"))
			stub := &diagnosticAdapterStub{cause: test.cause}
			Register(api, stub, stub, slog.New(slog.NewJSONHandler(&logs, nil)))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String()+fmt.Sprint(response.Header())+logs.String(), "SENSITIVE") {
				t.Fatal("private cause leaked")
			}
			if test.name == "invocation persistence" &&
				!strings.Contains(
					response.Body.String(),
					`"invocation_id":"afi_01890f47-7a6b-7c4d-8e9f-0123456789ab"`,
				) {
				t.Error("lost invocation receipt")
			}
			operation := "get-adapter"
			if test.name == "function read" {
				operation = "get-adapter-function"
			}
			if test.name == "invocation persistence" {
				operation = "invoke-adapter-function"
			}
			assertAdapterDiagnosticEvidence(t, logs.Bytes(), test.wantDiagnostic, operation)
		})
	}
}

func TestAdapterHTTPProblemsDirectlySatisfyStatusError(t *testing.T) {
	t.Parallel()
	cause := errors.New("SENSITIVE-cause")
	h := &handler{logger: slog.New(slog.DiscardHandler)}
	for _, problem := range []error{h.internalAPIError(t.Context(), "get-adapter", "service", cause), functionHTTPError(cause)} {
		if _, ok := problem.(huma.StatusError); !ok { //nolint:errorlint // The issue requires direct interface conformance, not unwrapping.
			t.Errorf("%T does not directly implement huma.StatusError", problem)
		}
		raw, err := json.Marshal(problem)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw)+problem.Error(), "SENSITIVE") {
			t.Fatal("private cause leaked")
		}
	}
}

func assertAdapterDiagnosticEvidence(t *testing.T, raw []byte, want bool, operation string) {
	t.Helper()
	if !want {
		if len(raw) != 0 {
			t.Errorf("expected silence, got %s", raw)
		}
		return
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Errorf("want exactly one safe diagnostic: %v", err)
	} else if record["level"] != "ERROR" || record["event"] != "adapter.http_internal_failure" ||
		record["operation"] != operation || record["stage"] != "service" || record["error_code"] != "internal_error" ||
		record["error_type"] == "" || len(record) != 8 {
		t.Errorf("unexpected diagnostic fields: %v", record)
	}
}

type diagnosticAdapterStub struct {
	Adapters
	Functions

	cause error
}

func (stub *diagnosticAdapterStub) GetAdapter(context.Context, string) (adapters.AdapterInstance, error) {
	return adapters.AdapterInstance{}, stub.cause
}

func (stub *diagnosticAdapterStub) GetFunction(
	context.Context,
	string,
	adapters.FunctionKey,
) (adapters.Function, error) {
	return adapters.Function{}, stub.cause
}
func (stub *diagnosticAdapterStub) Invoke(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
	return adapters.Invocation{}, stub.cause
}
