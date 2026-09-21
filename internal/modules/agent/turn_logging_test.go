package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// rejectedModelKey is the sentinel a failed model call carries. The log record
// must never contain it.
const rejectedModelKey = "sk-test-rejected-key-must-not-be-logged"

// lockedBuffer is a mutex-guarded log sink: the agent logs from the turn
// goroutine, and the test reads the record afterwards.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *lockedBuffer) Write(record []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(record)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

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

// TestTurnFailureCodeClassifiesCancellationAndValidation protects the fixed code
// set: a cancelled queued turn and a rejected message are distinguishable in
// logs without reading any error string.
func TestTurnFailureCodeClassifiesCancellationAndValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "cancelled", err: context.Canceled, want: "canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "deadline_exceeded"},
		{name: "unknown conversation", err: ErrConversationNotFound, want: "conversation_not_found"},
		{name: "closed admission", err: ErrAdmissionUnavailable, want: "admission_unavailable"},
		{name: "model call", err: turnError(errors.New("upstream text")), want: "model_call_failed"},
		{name: "persistence", err: errors.New("agent persist message: disk"), want: "turn_failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := turnFailureCode(testCase.err); got != testCase.want {
				t.Fatalf("turnFailureCode(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}
