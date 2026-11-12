//go:build integration

package agent //nolint:testpackage // Checks the provider wire after Eino search.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

// Synthetic peers prove the full compact MCP schema survives both provider
// adapters after search; they do not establish hosted-model behavior.
//
//nolint:gocognit // One comparable wire journey for both provider adapters.
func TestToolSearchDashboardProviderWire(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenCodeGo, ProviderOpenAI} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			server := dashboardMCPServer(dashboards.NewService(nil, nil, nil))
			want := dashboardAdvertisedSchema(t, server)
			rawSchema, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{`"$defs"`, `"$ref"`, `"$id"`, `"description"`} {
				if !strings.Contains(string(rawSchema), marker) {
					t.Fatalf("published compact schema missing %s", marker)
				}
			}
			requests := make(chan map[string]any, 2)
			var count atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if decodeErr := json.NewDecoder(r.Body).Decode(&request); decodeErr != nil {
					t.Error(decodeErr)
					return
				}
				requests <- request
				writeSearchWireResponse(w, provider, count.Add(1))
			}))
			defer peer.Close()
			store := newConversationStore(t, MinimumConversationRetention)
			service, err := NewService(
				t.Context(),
				Config{DB: store.database, Tools: mustBridgeTools(t, server), Model: ModelConfig{
					Provider: provider, APIKey: "test-key", BaseURL: peer.URL + "/v1", Model: "fixture",
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			conversation := mustCreateConversation(t, service)
			if _, err = service.SendMessage(
				t.Context(),
				conversation.ID,
				MessageInput{Text: "Show a view"},
			); err != nil {
				t.Fatal(err)
			}
			first, second := <-requests, <-requests
			assertSearchWireSchema(t, first, provider, nil)
			assertSearchWireSchema(t, second, provider, want)
			assertSearchWireContinuation(t, second, provider)
		})
	}
}

func writeSearchWireResponse(w http.ResponseWriter, provider Provider, step int32) {
	if provider == ProviderOpenCodeGo {
		w.Header().Set("Content-Type", "text/event-stream")
		if step == 1 {
			fmt.Fprint(
				w,
				goChatStream(
					"private-search-reasoning",
					"",
					`[{"index":0,"id":"wire-search","type":"function","function":{"name":"tool_search","arguments":"{\"query\":\"select:present_view\"}"}}]`,
					"tool_calls",
				),
			)
		} else {
			fmt.Fprint(w, goChatStream("private-final-reasoning", "Done.", "", "stop"))
		}
		return
	}
	output := responsesFinalFixture
	if step == 1 {
		output = `[{"id":"rs_search","type":"reasoning","summary":[],"encrypted_content":"private-search-reasoning"},{"id":"fc_search","type":"function_call","call_id":"wire-search","name":"tool_search","arguments":"{\"query\":\"select:present_view\"}","status":"completed"}]`
	}
	writeResponsesStream(
		w,
		fmt.Sprintf(
			`{"id":"resp_%d","object":"response","status":"completed","output":%s,"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
			step,
			output,
		),
	)
}

func assertSearchWireSchema(t *testing.T, request map[string]any, provider Provider, want map[string]any) {
	t.Helper()
	tools, ok := request["tools"].([]any)
	if !ok {
		t.Fatalf("missing wire tools: %v", request)
	}
	var foundSearch, foundSkill, foundView bool
	for _, item := range tools {
		entry, valid := item.(map[string]any)
		if !valid {
			t.Fatalf("invalid wire tool: %v", item)
		}
		if provider == ProviderOpenCodeGo {
			entry, _ = entry["function"].(map[string]any)
		}
		switch entry["name"] {
		case "skill":
			foundSkill = true
		case "tool_search":
			foundSearch = true
		case "present_view":
			foundView = true
			if entry["strict"] != false || !reflect.DeepEqual(entry["parameters"], want) {
				t.Errorf("wire present_view schema differs from advertised MCP schema")
			}
		}
	}
	if !foundSearch || !foundSkill || foundView != (want != nil) {
		t.Fatalf(
			"wire visibility: skill=%t search=%t view=%t expected view=%t",
			foundSkill,
			foundSearch,
			foundView,
			want != nil,
		)
	}
}

//nolint:gocognit // Compare native call, result and reasoning for both wire protocols.
func assertSearchWireContinuation(t *testing.T, request map[string]any, provider Provider) {
	t.Helper()
	var entries []any
	if provider == ProviderOpenCodeGo {
		entries, _ = request["messages"].([]any)
	} else {
		entries, _ = request["input"].([]any)
	}
	var reasoning, call, result bool
	for _, raw := range entries {
		item, _ := raw.(map[string]any)
		if provider == ProviderOpenCodeGo {
			if item["role"] == "assistant" && item["reasoning_content"] == "private-search-reasoning" {
				reasoning = true
				calls, _ := json.Marshal(item["tool_calls"])
				call = strings.Contains(string(calls), `"id":"wire-search"`) &&
					strings.Contains(string(calls), `"name":"tool_search"`)
			}
			result = result ||
				(item["role"] == "tool" && item["tool_call_id"] == "wire-search" && item["content"] == `{"matches":["present_view"]}`)
		} else {
			reasoning = reasoning ||
				(item["type"] == "reasoning" && item["encrypted_content"] == "private-search-reasoning")
			call = call ||
				(item["type"] == "function_call" && item["call_id"] == "wire-search" && item["name"] == "tool_search")
			result = result ||
				(item["type"] == "function_call_output" && item["call_id"] == "wire-search" && item["output"] == `{"matches":["present_view"]}`)
		}
	}
	if !reasoning || !call || !result {
		t.Errorf(
			"lost native search continuation: reasoning=%t call=%t result=%t input=%v",
			reasoning,
			call,
			result,
			entries,
		)
	}
}
