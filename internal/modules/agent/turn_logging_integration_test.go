//go:build integration

package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

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

	if _, sendErr := service.SendMessage(context.Background(), conversation.ID, "hello"); sendErr == nil {
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
