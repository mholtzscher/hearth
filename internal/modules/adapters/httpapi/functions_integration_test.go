//go:build integration

package httpapi //nolint:testpackage // Public registrations are exercised with shared transport fixtures.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

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
