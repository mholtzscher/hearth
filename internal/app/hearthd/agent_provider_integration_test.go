//go:build integration

package hearthd //nolint:testpackage // Exercises the required-agent production assembly seam.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardmcp "github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestAgentAssemblySelectsGoWithoutOpenAICredential(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "")
	t.Setenv("HEARTH_OPENCODE_GO_API_KEY", "  test-go-key\n")
	requests := make(chan string, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-go-key" ||
			body.Model != "deepseek-v4.1-flash" || body.ReasoningEffort != "max" {
			t.Error("Go provider settings or credential did not reach the provider")
		}
		requests <- r.Header.Get("X-Opencode-Session")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(
			w,
			"data: {\"id\":\"assembly\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Go ready.\",\"reasoning_content\":\"private\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		)
	}))
	defer peer.Close()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "assembly.db"))
	catalog := mcpapi.New(mcpapi.Config{Name: "agent-assembly-test", Version: "1"})
	dashboardmcp.Register(catalog, dashboards.NewService(nil, nil, nil))
	config := Config{
		Agent: AgentConfig{Provider: agent.ProviderOpenCodeGo, BaseURL: peer.URL + "/v1", ReasoningEffort: "max"},
	}
	service, err := startAgentService(t.Context(), config, catalog, database, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if drainErr := service.Drain(t.Context()); drainErr != nil {
			t.Error(drainErr)
		}
	}()
	conversation, err := service.CreateConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.SendMessage(t.Context(), conversation.ID, agent.MessageInput{Text: "Hello"})
	if err != nil || turn.Reply != "Go ready." {
		t.Fatalf("turn=%+v error=%v", turn, err)
	}
	if session := <-requests; session != conversation.ID {
		t.Fatalf("session=%q", session)
	}
}
