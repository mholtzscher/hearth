//go:build integration

package httpapi //nolint:testpackage // Public registrations are exercised with shared transport fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

//nolint:gocognit // One terminal matrix pins HTTP body, headers, and published schema independently.
func TestFunctionTerminalEvidenceHTTP(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status   string
		evidence adapters.Evidence
	}{
		{"succeeded", adapters.Succeeded{Result: json.RawMessage(functionArguments)}},
		{"failed", adapters.Failed{ReasonCode: "upstream_refused"}},
		{"not_executed", adapters.NotExecuted{ReasonCode: "adapter_unavailable"}},
		{"unknown", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			invocation := functionFixture(test.evidence)
			stub := &stubFunctions{
				invoke: func(_ context.Context, in adapters.InvokeInput) (adapters.Invocation, error) {
					if string(in.Arguments) != functionArguments {
						t.Fatalf("rounded arguments: %s", in.Arguments)
					}
					return invocation, nil
				},
				detail: func(_ context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
					if id != functionInvocationID {
						t.Fatalf("id=%s", id)
					}
					return invocation, nil
				},
				history: func(_ context.Context, filter adapters.InvocationFilter) (adapters.InvocationPage, error) {
					if filter.AdapterID != "demo" || filter.Limit != 50 {
						t.Fatalf("filter=%+v", filter)
					}
					return adapters.InvocationPage{Items: []adapters.Invocation{invocation}}, nil
				},
			}
			router, api := functionHTTP(t, stub)
			body := `{"catalog_revision":"` + functionRevision + `","arguments":` + functionArguments + `}`
			response := functionRequest(t, router, http.MethodPost, functionInvokePath, body)
			if response.Code != http.StatusOK ||
				response.Header().Get("Location") != "/v1/adapter-function-invocations/"+functionInvocationID {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			assertFunctionEvidence(t, response.Body.Bytes(), test.status)
			for _, path := range []string{"/v1/adapter-function-invocations/" + functionInvocationID, "/v1/adapters/demo/function-invocations"} {
				read := functionRequest(t, router, http.MethodGet, path, "")
				if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"status":"`+test.status+`"`) ||
					!strings.Contains(read.Body.String(), "9007199254740993") {
					t.Fatalf("read=%d %s", read.Code, read.Body.String())
				}
			}
			operation := api.OpenAPI().Paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post
			if operation == nil || operation.OperationID != "invoke-adapter-function" {
				t.Fatalf("operation=%+v", operation)
			}
			openapiSchema := compileFunctionSchema(t, operation.Responses["200"].Content["application/json"].Schema)
			functionSchemaAccepts(t, openapiSchema, response.Body.String(), true)
			contradictory := strings.TrimSuffix(response.Body.String(), "\n")
			contradictory = strings.TrimSuffix(contradictory, "}") + `,"result":{},"reason_code":"contradictory"}`
			functionSchemaAccepts(t, openapiSchema, contradictory, false)
		})
	}
}

func TestFunctionErrorsAreSafeAndRetainKnownInvocationID(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{adapters.ErrInvalidFunctionInput, 422, "invalid_input"}, {adapters.ErrAdapterNotFound, 404, "adapter_not_found"},
		{adapters.ErrFunctionNotFound, 404, "function_not_found"}, {adapters.ErrInvocationNotFound, 404, "invocation_not_found"},
		{adapters.ErrCatalogChanged, 409, "catalog_changed"}, {adapters.ErrCatalogConflict, 409, "catalog_conflict"},
		{adapters.ErrAdapterUnavailable, 503, "adapter_unavailable"}, {adapters.ErrFunctionAdmissionClosed, 503, "admission_closed"},
		{adapters.ErrFunctionPersistence, 500, "internal_error"}, {errors.New("secret-database-path"), 500, "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: adapters.ErrFunctionPersistence}, 500, "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: errors.Join(adapters.ErrFunctionPersistence, adapters.ErrInvalidFunctionInput)}, 500, "internal_error"},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			t.Parallel()
			stub := &stubFunctions{invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
				return adapters.Invocation{}, fmt.Errorf("secret-validator-value: %w", test.err)
			}}
			router, _ := functionHTTP(t, stub)
			response := functionRequest(
				t,
				router,
				http.MethodPost,
				functionInvokePath,
				`{"catalog_revision":"`+functionRevision+`","arguments":{}}`,
			)
			if response.Code != test.status || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			var known *adapters.InvocationError
			wantID := errors.As(test.err, &known)
			if strings.Contains(response.Body.String(), functionInvocationID) != wantID {
				t.Fatalf("known ID lost: %s", response.Body.String())
			}
		})
	}
}
