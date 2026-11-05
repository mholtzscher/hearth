//go:build e2e

package hearthd //nolint:testpackage // Verifies actual Core assembly, SDK and agent transport boundaries.

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// A6/A8/A9: real Core shutdown must withdraw readiness without abandoning an
// admitted function. Restart must retain its catalog and success, recover an
// interrupted admission as unknown, and never replay that admission.
func TestFunctionIntegrationCoreDrainAndRestartWithoutReplay(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	config := Config{
		HouseholdTimezone: "UTC",
		NATSURL:           broker.ClientURL(),
		SQLitePath:        dbtest.MigratedPath(t),
		Agent:             requiredAgentConfig(t),
	}
	first := startFunctionCore(t, config)
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "restart-functions",
			SoftwareName:    "controlled-functions",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Registered after the first Core's cleanup, so close while Core can release.
	t.Cleanup(func() { _ = session.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	_, err = session.StartFunctionServer(
		t.Context(),
		[]adapter.Function{
			exactIntegrationFunction(func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				return adapter.FunctionSucceeded{Result: request.Arguments}
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := newNonPoolingHTTPClient(t)
	path := "/v1/adapters/restart-functions/functions/inspect"
	raw := successfulFunctionCoreRequest(t, client, first, http.MethodGet, path, "")
	function := decodeNotification[adaptersapi.FunctionBody](t, raw)
	body := `{"catalog_revision":"` + function.CatalogRevision + `","arguments":` + functionExactObject + `}`
	responseDone := make(chan struct{})
	var invokeStatus int
	var invokeRaw []byte
	var invokeErr error
	go func() {
		invokeStatus, invokeRaw, invokeErr = functionNetworkRequest(
			t.Context(),
			client,
			first.address,
			http.MethodPost,
			path+"/invocations",
			body,
		)
		close(responseDone)
	}()
	t.Cleanup(func() {
		unblock()
		select {
		case <-responseDone:
		case <-time.After(10 * time.Second):
			t.Error("HTTP invocation did not join")
		}
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("SDK handler never started")
	}
	first.stop()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		code, _, requestErr := functionNetworkRequest(t.Context(), client, first.address, http.MethodGet, "/readyz", "")
		return requestErr == nil && code == http.StatusServiceUnavailable, nil
	})
	select {
	case <-first.done:
		t.Fatalf("Core returned before handler completion: %v", first.err)
	default:
	}
	unblock()
	select {
	case <-responseDone:
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP invocation did not finish")
	}
	if invokeErr != nil || invokeStatus != http.StatusOK {
		t.Fatalf("draining invocation: %d %s %v", invokeStatus, invokeRaw, invokeErr)
	}
	completed := decodeNotification[adaptersapi.InvocationBody](t, invokeRaw)
	assertExactFunctionInvocation(t, completed)
	first.join(t)
	interruptedID := seedInterruptedFunctionInvocation(t, config.SQLitePath)
	second := startFunctionCore(t, config)
	t.Cleanup(func() { _ = session.Close() })
	// Session and function server deliberately remain alive: no re-registration.
	raw = successfulFunctionCoreRequest(t, client, second, http.MethodGet, path, "")
	after := decodeNotification[adaptersapi.FunctionBody](t, raw)
	if !after.Callable || after.CatalogRevision != function.CatalogRevision {
		t.Fatalf("restart catalog: %+v", after)
	}
	assertRecoveredFunctionInvocation(
		t,
		client,
		second,
		string(interruptedID),
		function.CatalogRevision,
		calls.Load(),
	)
	raw = successfulFunctionCoreRequest(
		t,
		client,
		second,
		http.MethodGet,
		"/v1/adapter-function-invocations/"+completed.ID,
		"",
	)
	assertExactFunctionInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, raw))
	raw = successfulFunctionCoreRequest(
		t,
		client,
		second,
		http.MethodPost,
		path+"/invocations",
		body,
	)
	assertExactFunctionInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, raw))
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d, want only explicit invocations", calls.Load())
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	second.stop()
	second.join(t)
}

// assertRecoveredFunctionInvocation proves restart recorded a truthful unknown
// outcome for the synthetic interrupted admission without replaying it, while
// preserving its exact route, arguments, and backward diagnostic time.
func assertRecoveredFunctionInvocation(
	t *testing.T,
	client *http.Client,
	core *functionCoreRun,
	interruptedID, catalogRevision string,
	calls int32,
) {
	t.Helper()
	recovered := decodeNotification[adaptersapi.InvocationBody](
		t,
		successfulFunctionCoreRequest(
			t,
			client,
			core,
			http.MethodGet,
			"/v1/adapter-function-invocations/"+interruptedID,
			"",
		),
	)
	if recovered.ID != interruptedID || recovered.AdapterID != "restart-functions" ||
		recovered.FunctionKey != "inspect" || recovered.CatalogRevision != catalogRevision ||
		string(recovered.Arguments) != functionExactObject ||
		recovered.Status != "unknown" || recovered.ReasonCode != "core_restarted" ||
		recovered.CompletedAt == nil || len(recovered.Result) != 0 || calls != 1 {
		t.Fatalf("recovery replayed or lost evidence: %+v calls=%d", recovered, calls)
	}
	recoveredAt, err := time.Parse(time.RFC3339Nano, *recovered.CompletedAt)
	if err != nil {
		t.Fatal(err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, recovered.StartedAt)
	if err != nil || !recoveredAt.Before(startedAt) {
		t.Fatalf("restart did not preserve backward diagnostic time: %+v", recovered)
	}
}
