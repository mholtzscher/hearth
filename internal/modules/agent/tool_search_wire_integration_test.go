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

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

// Synthetic peers prove the full native schema survives both provider
// adapters after search; they do not establish hosted-model behavior.
func TestToolSearchDashboardProviderWire(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenCodeGo, ProviderOpenAI} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			var want map[string]any
			if err := json.Unmarshal(composeViewSchema, &want); err != nil {
				t.Fatal(err)
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
				Config{DB: store.database, ViewAuthoring: dashboards.NewService(nil, nil, nil), Model: ModelConfig{
					Provider: provider, APIKey: "test-key", BaseURL: peer.URL + "/v1", Model: "fixture",
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			conversation := mustCreateConversation(t, service)
			legacy := seedLegacyDashboardHistory(t, service, conversation.ID, provider)
			turn, err := service.SendMessage(
				t.Context(),
				conversation.ID,
				MessageInput{Text: "Show a view"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(turn.ToolCalls, []string{"tool_search"}) {
				t.Fatalf("new turn executed a historical call: %v", turn.ToolCalls)
			}
			first, second := <-requests, <-requests
			assertSearchWireSchema(t, first, provider, nil)
			assertSearchWireSchema(t, second, provider, want)
			assertSearchWireContinuation(t, second, provider)
			assertLegacyDashboardHistory(t, service, conversation.ID, first, provider, legacy)
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
					`[{"index":0,"id":"wire-search","type":"function","function":{"name":"tool_search","arguments":"{\"query\":\"select:compose_view,present_view\"}"}}]`,
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
		output = `[{"id":"rs_search","type":"reasoning","summary":[],"encrypted_content":"private-search-reasoning"},{"id":"fc_search","type":"function_call","call_id":"wire-search","name":"tool_search","arguments":"{\"query\":\"select:compose_view,present_view\"}","status":"completed"}]`
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
		case legacyViewToolName:
			t.Error("wire advertised retired present_view")
		case composeViewToolName:
			foundView = true
			if entry["strict"] != false || !reflect.DeepEqual(entry["parameters"], want) {
				t.Errorf("wire compose_view schema differs from its recursive input contract")
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
				(item["role"] == "tool" && item["tool_call_id"] == "wire-search" && item["content"] == `{"matches":["compose_view"]}`)
		} else {
			reasoning = reasoning ||
				(item["type"] == "reasoning" && item["encrypted_content"] == "private-search-reasoning")
			call = call ||
				(item["type"] == "function_call" && item["call_id"] == "wire-search" && item["name"] == "tool_search")
			result = result ||
				(item["type"] == "function_call_output" && item["call_id"] == "wire-search" && item["output"] == `{"matches":["compose_view"]}`)
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

func seedLegacyDashboardHistory(t *testing.T, service *Service, id string, provider Provider) dashboards.Artifact {
	t.Helper()
	raw := dashboardDefinitionFixture(t, "Legacy dashboard", false)
	const artifactID = "view_018f0f4a-7b29-7000-8000-000000000001"
	artifact := dashboards.Artifact{Kind: "hearth.view", ArtifactID: artifactID, Definition: raw}
	output, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	args := `{"definition":` + string(raw) + `}`
	search := legacyProviderCall(provider, "old-search", "tool_search", `{"query":"select:present_view"}`)
	view := legacyProviderCall(provider, "old-view", legacyViewToolName, args)
	for _, message := range []*schema.Message{
		newUserMessage(MessageInput{Text: "Show the old view"}, provider),
		search,
		{Role: schema.Tool, ToolCallID: "old-search", ToolName: "tool_search", Content: `{"matches":["present_view"]}`},
		view,
		{Role: schema.Tool, ToolCallID: "old-view", ToolName: legacyViewToolName, Content: string(output)},
	} {
		if persistErr := service.persistMessage(t.Context(), id, message); persistErr != nil {
			t.Fatal(persistErr)
		}
	}
	return artifact
}

func legacyProviderCall(provider Provider, id, name, args string) *schema.Message {
	message := schema.AssistantMessage("", []schema.ToolCall{nativeTestCall(id, name, args)})
	if provider == ProviderOpenCodeGo {
		message.Extra = map[string]any{goChatMessageKey: fmt.Sprintf(
			`{"role":"assistant","content":null,"reasoning_content":"retained-legacy-reasoning","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]}`,
			id,
			name,
			args,
		)}
	} else {
		message.Extra = map[string]any{responsesOutputKey: fmt.Sprintf(
			`[{"id":"rs_%s","type":"reasoning","summary":[],"encrypted_content":"retained-legacy-reasoning"},{"id":"fc_%s","type":"function_call","call_id":%q,"name":%q,"arguments":%q,"status":"completed"}]`,
			id,
			id,
			id,
			name,
			args,
		)}
	}
	return message
}

func assertLegacyDashboardHistory(
	t *testing.T, service *Service, id string, request map[string]any, provider Provider, expected dashboards.Artifact,
) {
	t.Helper()
	input := request["input"]
	if provider == ProviderOpenCodeGo {
		input = request["messages"]
	}
	raw, err := json.Marshal(input)
	if err != nil || !strings.Contains(string(raw), "retained-legacy-reasoning") ||
		!strings.Contains(string(raw), "old-view") {
		t.Errorf("provider continuation lost legacy call identity or private metadata: %v", err)
	}
	history, err := service.History(t.Context(), id)
	if err != nil || len(history) < 5 || history[4].ToolCallID != "old-view" ||
		history[4].ToolName != legacyViewToolName || len(history[4].Artifacts) != 1 ||
		!reflect.DeepEqual(history[4].Artifacts[0].Artifact, expected) {
		t.Fatalf("legacy artifact/identity did not survive: %+v, %v", history, err)
	}
}
