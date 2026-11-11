//go:build integration

package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// TestLogTurnFailureRecordsFixedCodesOnly protects the logging posture in
// docs/logging.md: an unknown error string can carry a prompt, rejected value,
// or credential, so agent.turn_failed records a fixed stage and error code
// instead. It fails if the raw model error text reaches the log.
func TestLogTurnFailureRecordsFixedCodesOnly(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	service, err := NewService(context.Background(), Config{
		DB:        database,
		Tools:     []tool.BaseTool{stubTool{name: "noop"}},
		ChatModel: failingChatModel{err: fmt.Errorf("provider rejected key %s", rejectedModelKey)},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)

	if _, sendErr := service.SendMessage(
		context.Background(),
		conversation.ID,
		MessageInput{Text: "hello"},
	); sendErr == nil {
		t.Fatal("SendMessage succeeded, want the model failure")
	}
	record := logs.String()
	for _, want := range []string{
		`"event":"agent.turn_failed"`,
		`"stage":"model"`,
		`"error_code":"model_call_failed"`,
		`"conversation_id":"` + conversation.ID + `"`,
	} {
		if !strings.Contains(record, want) {
			t.Fatalf("log record %s is missing %s", record, want)
		}
	}
	if strings.Contains(record, rejectedModelKey) {
		t.Fatalf("log record leaked the raw error text: %s", record)
	}
	if strings.Contains(record, `"error"`) {
		t.Fatalf("log record carries a raw error field: %s", record)
	}
}

// usageChatModel answers once with provider-reported token usage.
type usageChatModel struct{ failingChatModel }

func (usageChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	message := schema.AssistantMessage("private-answer", nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens: 2048, CompletionTokens: 16, TotalTokens: 2064,
		PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 1536, CacheWriteTokens: 512},
	}}
	return message, nil
}

func TestModelUsageIsLoggedAtDebugWithoutConversationText(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	service, err := NewService(context.Background(), Config{
		DB:        dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db")),
		Tools:     []tool.BaseTool{stubTool{name: "noop"}},
		ChatModel: usageChatModel{},
		Logger:    slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	if _, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "private-prompt"}); err != nil {
		t.Fatal(err)
	}
	record := logs.String()
	for _, want := range []string{
		`"level":"DEBUG"`, `"event":"agent.model_usage"`, `"conversation_id":"` + conversation.ID + `"`,
		`"provider":"openai"`, `"input_tokens":2048`, `"cached_input_tokens":1536`,
		`"cache_write_input_tokens":512`, `"output_tokens":16`,
	} {
		if !strings.Contains(record, want) {
			t.Fatalf("log record %s is missing %s", record, want)
		}
	}
	if strings.Contains(record, "private-") {
		t.Fatalf("log record leaked conversation text: %s", record)
	}
}
