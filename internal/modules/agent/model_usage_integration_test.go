//go:build integration

package agent //nolint:testpackage // Verifies private SQLite retention and per-call diagnostics through the real service.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

func TestModelUsageIsPrivateDurableAndLoggedOncePerCall(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenAI, ProviderOpenCodeGo} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(string(body), modelUsageKey) {
					t.Error("usage metadata leaked into model input")
				}
				details := `{"cached_tokens":0,"cache_write_tokens":512}`
				if calls.Add(1) == 2 {
					details = `{"cached_tokens":1536,"cache_write_tokens":null}`
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, usageStreamFixture(provider, details, true))
			}))
			defer peer.Close()
			var logs lockedBuffer
			store := newConversationStore(t, MinimumConversationRetention)
			config := Config{
				DB:     store.database,
				Tools:  []tool.BaseTool{stubTool{name: "noop"}},
				Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Model: ModelConfig{
					Provider: provider,
					Model:    "usage-fixture",
					BaseURL:  peer.URL,
					APIKey:   "private-api-key",
				},
			}
			service, err := NewService(t.Context(), config)
			require.NoError(t, err)
			conversation := mustCreateConversation(t, service)
			_, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "private-first-prompt"})
			require.NoError(t, err)
			// Recreate the service to ensure reloaded usage is neither sent upstream
			// nor emitted again as a fresh model call.
			service, err = NewService(t.Context(), config)
			require.NoError(t, err)
			_, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "private-second-prompt"})
			require.NoError(t, err)
			require.Equal(t, int32(2), calls.Load())
			assertRetainedModelUsage(t, service, conversation.ID)
			assertModelUsageLogs(t, logs.String(), provider, conversation.ID)
			router := newOperationsRouter(service)
			public := httptest.NewRecorder()
			router.ServeHTTP(
				public,
				httptest.NewRequest(http.MethodGet, "/v1/agent/conversations/"+conversation.ID+"/messages", nil),
			)
			require.Equal(t, http.StatusOK, public.Code)
			require.Contains(t, public.Body.String(), "private-answer")
			require.NotContains(t, public.Body.String(), modelUsageKey)
			require.NotContains(t, public.Body.String(), "cached_input_tokens")
		})
	}
}

func assertRetainedModelUsage(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	rows, err := service.queries.ListMessages(t.Context(), dbsqlc.ListMessagesParams{ConversationID: conversationID})
	require.NoError(t, err)
	var retained []modelUsage
	for _, row := range rows {
		if row.Role != "assistant" {
			continue
		}
		var message struct {
			Extra map[string]json.RawMessage `json:"extra"`
		}
		require.NoError(t, json.Unmarshal([]byte(row.MessageJson), &message))
		var usage modelUsage
		require.NoError(t, json.Unmarshal(message.Extra[modelUsageKey], &usage))
		retained = append(retained, usage)
	}
	require.Len(t, retained, 2)
	require.Equal(t, new(int64(0)), retained[0].CachedInputTokens)
	require.Equal(t, new(int64(512)), retained[0].CacheWriteInputTokens)
	require.Equal(t, new(int64(1536)), retained[1].CachedInputTokens)
	require.Nil(t, retained[1].CacheWriteInputTokens)
	require.Equal(t, new(int64(2048)), retained[1].InputTokens)
}

func assertModelUsageLogs(t *testing.T, output string, provider Provider, conversationID string) {
	t.Helper()
	require.NotContains(t, output, "private-")
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		var record map[string]any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		require.NoError(t, decoder.Decode(&record))
		if record["event"] == "agent.model_usage" {
			records = append(records, record)
		}
	}
	require.Len(t, records, 2)
	for _, record := range records {
		require.Equal(t, "DEBUG", record["level"])
		require.Equal(t, string(provider), record["provider"])
		require.Equal(t, "usage-fixture", record["model"])
		require.Equal(t, conversationID, record["conversation_id"])
		require.Equal(t, json.Number("2048"), record["input_tokens"])
		require.Equal(t, json.Number("16"), record["output_tokens"])
		require.Equal(t, json.Number("2064"), record["total_tokens"])
	}
	require.Equal(t, json.Number("0"), records[0]["cached_input_tokens"])
	require.Equal(t, json.Number("512"), records[0]["cache_write_input_tokens"])
	require.Equal(t, json.Number("1536"), records[1]["cached_input_tokens"])
	require.NotContains(t, records[1], "cache_write_input_tokens")
}
