//nolint:testpackage // Exercises app-owned control routing for held function invocations.
package simulator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func functionControlRuntime(t *testing.T) *scripted.FunctionRuntime {
	t.Helper()
	runtime, err := scripted.NewFunctionRuntime([]scripted.FunctionSpec{{
		Key: "hold", Description: "Hold",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Hold:         true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

// Release is the only function control. It reports 404 before admission, 204
// once a real held handler is running, and 404 again after that handler returns
// so Core history, not this channel, owns completion evidence.
func TestFunctionControlReleasesHeldInvocation(t *testing.T) {
	t.Parallel()
	runtime := functionControlRuntime(t)
	mux := http.NewServeMux()
	registerFunctionControls(mux, map[string]*scripted.FunctionRuntime{"functions": runtime})

	unknown := httptest.NewRecorder()
	mux.ServeHTTP(
		unknown,
		httptest.NewRequest(http.MethodPost, "/v1/sim/functions/invocations/afi_unknown/release", nil),
	)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown invocation status = %d", unknown.Code)
	}

	ctx, cancel := context.WithTimeout(t.Context(), sessionFailureWait)
	defer cancel()
	done := make(chan adapter.FunctionResult, 1)
	go func() {
		done <- runtime.Functions()[0].Handler(ctx, adapter.FunctionRequest{InvocationID: "afi_exact"})
	}()
	released := false
	for !released {
		response := httptest.NewRecorder()
		mux.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/v1/sim/functions/invocations/afi_exact/release", nil),
		)
		switch response.Code {
		case http.StatusNoContent:
			released = true
		case http.StatusNotFound:
			select {
			case <-ctx.Done():
				t.Fatal("held invocation was never admitted")
			case <-time.After(time.Millisecond):
			}
		default:
			t.Fatalf("release status = %d", response.Code)
		}
	}
	select {
	case result := <-done:
		if _, ok := result.(adapter.FunctionSucceeded); !ok {
			t.Fatalf("released result = %#v", result)
		}
	case <-ctx.Done():
		t.Fatal("release did not unblock the handler")
	}
	completed := httptest.NewRecorder()
	mux.ServeHTTP(
		completed,
		httptest.NewRequest(http.MethodPost, "/v1/sim/functions/invocations/afi_exact/release", nil),
	)
	if completed.Code != http.StatusNotFound {
		t.Fatalf("completed invocation status = %d", completed.Code)
	}
}
