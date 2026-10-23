//go:build integration

package httpapi //nolint:testpackage // Discovery contracts are checked through public registration.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionDiscoveryRoutesPreserveCatalogData(t *testing.T) {
	t.Parallel()
	descriptor := functionFixture(nil).Function
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740993}}}`)
	function := adapters.Function{
		ContractFingerprint: "sha256-v1:f527a6cf069eea12f79d697c3895935a3e605ef3d07e244524a5472ab34058c3",
		AdapterID:           "demo",
		CatalogRevision:     functionRevision,
		Descriptor:          descriptor,
		Callable:            false,
	}
	stub := &stubFunctions{
		list: func(_ context.Context, adapterID string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if adapterID != "demo" || page.Limit != 2 || page.Cursor != "opaque-scoped-cursor" {
				t.Fatalf("list=%s %+v", adapterID, page)
			}
			return adapters.FunctionPage{Items: []adapters.Function{function}, NextCursor: "next-scoped-cursor"}, nil
		},
		get: func(_ context.Context, adapterID string, key adapters.FunctionKey) (adapters.Function, error) {
			if adapterID != "demo" || key != "inspect" {
				t.Fatalf("get=%s %s", adapterID, key)
			}
			return function, nil
		},
	}
	router, api := functionHTTP(t, stub)
	for _, path := range []string{"/v1/adapters/demo/functions?limit=2&cursor=opaque-scoped-cursor", "/v1/adapters/demo/functions/inspect"} {
		response := functionRequest(t, router, http.MethodGet, path, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"const":9007199254740993`) ||
			!strings.Contains(response.Body.String(), `"callable":false`) ||
			!strings.Contains(
				response.Body.String(),
				`"contract_fingerprint":"`+string(function.ContractFingerprint)+`"`,
			) {
			t.Fatalf("discovery=%d %s", response.Code, response.Body.String())
		}
	}
	requestSchema := api.OpenAPI().Paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post.RequestBody.Content["application/json"].Schema
	compiled := compileFunctionSchema(t, requestSchema)
	functionSchemaAccepts(t, compiled, `{"catalog_revision":"x","arguments":{}}`, true)
	for _, invalid := range []string{`{}`, `{"catalog_revision":null,"arguments":{}}`, `{"catalog_revision":"x","arguments":null}`, `{"catalog_revision":"x","arguments":{},"extra":true}`} {
		functionSchemaAccepts(t, compiled, invalid, false)
	}
}

func TestFunctionHistoryForwardsFilterAndCursorAndExposesRunningNull(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		history: func(_ context.Context, filter adapters.InvocationFilter) (adapters.InvocationPage, error) {
			if filter.AdapterID != "demo" || filter.FunctionKey != "removed" || filter.Limit != 200 ||
				filter.Cursor != "history-cursor" {
				t.Fatalf("filter=%+v", filter)
			}
			return adapters.InvocationPage{Items: []adapters.Invocation{functionFixture(nil)}}, nil
		},
	}
	router, _ := functionHTTP(t, stub)
	response := functionRequest(
		t,
		router,
		http.MethodGet,
		"/v1/adapters/demo/function-invocations?function_key=removed&limit=200&cursor=history-cursor",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("history=%d %s", response.Code, response.Body.String())
	}
	want := response.Body.String()
	if !strings.Contains(want, `"completed_at":null`) || !strings.Contains(want, `"status":"running"`) ||
		strings.Contains(want, `"result"`) ||
		strings.Contains(want, `"reason_code"`) ||
		strings.Contains(want, `"next_cursor"`) {
		t.Fatalf("running shape=%s", want)
	}
}

func TestFunctionEmptyCollectionsUseArraysAndDefaultPageSize(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		list: func(_ context.Context, id string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if id != "demo" || page.Limit != 50 {
				t.Fatalf("list=%s %+v", id, page)
			}
			return adapters.FunctionPage{}, nil
		},
	}
	router, _ := functionHTTP(t, stub)
	response := functionRequest(t, router, http.MethodGet, "/v1/adapters/demo/functions", "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty collection=%d %s", response.Code, response.Body.String())
	}
	for _, limit := range []string{"0", "201", "not-an-integer"} {
		rejected := functionRequest(t, router, http.MethodGet, "/v1/adapters/demo/functions?limit="+limit, "")
		if rejected.Code != http.StatusUnprocessableEntity {
			t.Fatalf("limit=%s status=%d body=%s", limit, rejected.Code, rejected.Body.String())
		}
	}
}
