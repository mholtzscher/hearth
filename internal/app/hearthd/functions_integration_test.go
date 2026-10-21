//go:build integration

package hearthd //nolint:testpackage // Verifies actual Core assembly, SDK and agent transport boundaries.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	adaptersmcp "github.com/mholtzscher/hearth/internal/modules/adapters/mcpapi"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// A2/A9/A10: a fixed agent bridge created before registration must see the
// committed catalog and invoke its already-serving SDK subscription before Start
// returns. A lost acknowledgement must not change revision or duplicate rows.
// Exact literals above float64's precision and at both exponent boundaries detect
// lossy decoding in the real Eino -> official MCP -> service -> NATS -> SDK path.
//
//nolint:gocognit // Keeps one lost-acknowledgement scenario and its durable transport evidence together.
func TestFunctionIntegrationLateCatalogAndExactAgentInvocation(t *testing.T) {
	t.Parallel()
	f := newFunctionIntegrationFixture(t)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(f.registrar.release) }) }
	var calls atomic.Int32
	started := make(chan struct{})
	var startErr error
	go func() {
		_, startErr = f.session.StartFunctionServer(t.Context(), []adapter.Function{exactIntegrationFunction(
			func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
				calls.Add(1)
				return adapter.FunctionSucceeded{Result: request.Arguments}
			})})
		close(started)
	}()
	t.Cleanup(func() {
		release()
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Error("function server start did not join")
		}
	})
	var revision adapters.CatalogID
	select {
	case revision = <-f.registrar.committed:
	case <-time.After(10 * time.Second):
		t.Fatal("catalog did not commit")
	}
	listed := decodeNotification[adaptersmcp.FunctionPage](
		t,
		[]byte(runFunctionBridge(t, f.tools, "list_adapter_functions", `{"adapter_id":"function-only"}`)),
	)
	if len(listed.Items) != 1 || listed.Items[0].CatalogRevision != string(revision) || !listed.Items[0].Callable {
		t.Fatalf("late catalog: %+v", listed)
	}
	described := decodeNotification[adaptersmcp.FunctionDetail](
		t,
		[]byte(
			runFunctionBridge(
				t,
				f.tools,
				"get_adapter_function",
				`{"adapter_id":"function-only","function_key":"inspect"}`,
			),
		),
	)
	if described.CatalogRevision != string(revision) || described.FunctionKey != "inspect" || !described.Callable {
		t.Fatalf("description: %+v", described)
	}
	arguments := `{"adapter_id":"function-only","function_key":"inspect","catalog_revision":"` + string(
		revision,
	) + `","arguments":` + functionExactObject + `}`
	invocation := decodeNotification[adaptersmcp.InvocationSuccess](
		t,
		[]byte(runFunctionBridge(t, f.tools, "invoke_adapter_function", arguments)),
	)
	assertExactFunctionMCPReceipt(t, invocation)
	retained := decodeNotification[adaptersmcp.InvocationDetail](t, []byte(runFunctionBridge(t, f.tools,
		"get_adapter_function_invocation", `{"invocation_id":"`+invocation.InvocationID+`"}`)))
	assertExactFunctionMCPDetail(t, retained, invocation.InvocationID)
	select {
	case <-started:
		t.Fatalf("Start returned before registration reply release: %v", startErr)
	default:
	}
	release()
	select {
	case <-started:
		if startErr != nil {
			t.Fatal(startErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SDK did not retry registration")
	}
	f.registrar.mu.Lock()
	ids := append([]adapters.CatalogID(nil), f.registrar.ids...)
	f.registrar.mu.Unlock()
	if len(ids) < 2 {
		t.Fatalf("registration attempts: %v", ids)
	}
	for _, id := range ids {
		if id != revision {
			t.Fatalf("registration changed revision: %v", ids)
		}
	}
	var count int
	if err := f.database.QueryRowContext(t.Context(), `SELECT count(*) FROM adapter_function_catalogs WHERE catalog_revision = ?`, string(revision)).
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("catalog rows = %d: %v", count, err)
	}
	response := notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/adapters/function-only/functions/inspect/invocations",
		`{"catalog_revision":"`+string(revision)+`","arguments":`+functionExactObject+`}`,
		http.StatusOK,
	)
	httpInvocation := decodeNotification[adaptersapi.InvocationBody](t, response.Body.Bytes())
	assertExactFunctionInvocation(t, httpInvocation)
	if httpInvocation.ID == invocation.InvocationID ||
		response.Header().Get("Location") != "/v1/adapter-function-invocations/"+httpInvocation.ID {
		t.Fatalf("distinct invocation/location: %+v %s", httpInvocation, response.Header().Get("Location"))
	}
	for _, id := range []string{invocation.InvocationID, httpInvocation.ID} {
		saved := notificationRequest(
			t,
			f.handler,
			http.MethodGet,
			"/v1/adapter-function-invocations/"+id,
			"",
			http.StatusOK,
		)
		assertExactFunctionInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, saved.Body.Bytes()))
	}
	if calls.Load() != 2 {
		t.Fatalf("handler executions = %d, want two distinct manual invocations", calls.Load())
	}
	if err := f.session.Close(); err != nil {
		t.Fatal(err)
	}
	offline := decodeNotification[adaptersapi.FunctionCollectionBody](t,
		[]byte(runFunctionBridge(t, f.tools, "list_adapter_functions", `{"adapter_id":"function-only"}`)))
	if len(offline.Items) != 1 || offline.Items[0].Callable || offline.Items[0].CatalogRevision != string(revision) {
		t.Fatalf("offline catalog was removed or remained callable: %+v", offline)
	}
}

// A9/A13: a pre-Function database upgrades without losing existing data, then
// registered SDK work becomes readable retained history. The shared scheduler
// must apply the Function owner's strict thirty-day terminal-only cutoff, not
// delete a catalog or interpret still-running evidence as queued work.
func TestFunctionIntegrationUpgradeAndSharedHistoryPruning(t *testing.T) {
	t.Parallel()
	f := newFunctionIntegrationFixture(t)
	close(f.registrar.release)
	var calls atomic.Int32
	_, err := f.session.StartFunctionServer(t.Context(), []adapter.Function{exactIntegrationFunction(
		func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
			calls.Add(1)
			return adapter.FunctionSucceeded{Result: request.Arguments}
		})})
	if err != nil {
		t.Fatal(err)
	}
	function, err := f.service.GetFunction(t.Context(), "function-only", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	input := adapters.InvokeInput{AdapterID: function.AdapterID, FunctionKey: function.Descriptor.Key,
		CatalogRevision: function.CatalogRevision, Arguments: json.RawMessage(functionExactObject)}
	completed, err := f.service.Invoke(t.Context(), input)
	if err != nil || completed.Completion == nil {
		t.Fatalf("completed invocation: %+v %v", completed, err)
	}
	prepared, err := adapters.PrepareInvocation(function, input, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	running, err := adapterssqlite.NewAdapterRepository(f.database).Admit(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := newHistoryPruneScheduler(slog.New(slog.DiscardHandler), newHistoryPruneTasks(
		&stubHistoryPruner{name: devicesHistoryPruneModule},
		&stubHistoryPruner{name: automationsHistoryPruneModule},
		&stubHistoryPruner{
			name: agentHistoryPruneModule,
		},
		&stubHistoryPruner{name: notificationsHistoryPruneModule},
		f.service,
	))
	boundary := completed.Completion.At.Add(30 * 24 * time.Hour)
	scheduler.runPass(t.Context(), boundary)
	saved := notificationRequest(
		t,
		f.handler,
		http.MethodGet,
		"/v1/adapter-function-invocations/"+string(completed.ID),
		"",
		http.StatusOK,
	)
	assertExactFunctionInvocation(t, decodeNotification[adaptersapi.InvocationBody](t, saved.Body.Bytes()))
	scheduler.runPass(t.Context(), boundary.Add(time.Nanosecond))
	notificationRequest(
		t,
		f.handler,
		http.MethodGet,
		"/v1/adapter-function-invocations/"+string(completed.ID),
		"",
		http.StatusNotFound,
	)
	page := decodeNotification[adaptersapi.InvocationCollectionBody](
		t,
		notificationRequest(t, f.handler, http.MethodGet,
			"/v1/adapters/function-only/function-invocations", "", http.StatusOK).Body.Bytes(),
	)
	if len(page.Items) != 1 || page.Items[0].ID != string(running.ID) || page.Items[0].Status != "running" ||
		page.Items[0].CompletedAt != nil {
		t.Fatalf("pruning removed running evidence: %+v", page)
	}
	described := decodeNotification[adaptersapi.FunctionBody](t, notificationRequest(t, f.handler, http.MethodGet,
		"/v1/adapters/function-only/functions/inspect", "", http.StatusOK).Body.Bytes())
	if !described.Callable || described.CatalogRevision != string(function.CatalogRevision) || calls.Load() != 1 {
		t.Fatalf("pruning changed catalog or executed history: %+v calls=%d", described, calls.Load())
	}
	var createdAt string
	if err = f.database.QueryRowContext(t.Context(), `SELECT created_at FROM agent_conversations WHERE id = 'aconv_before_functions'`).
		Scan(&createdAt); err != nil ||
		createdAt != "2026-01-02T03:04:05.000000000Z" {
		t.Fatalf("upgrade changed existing conversation: %q %v", createdAt, err)
	}
}
