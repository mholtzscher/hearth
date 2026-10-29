package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
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
		{name: "model call", err: modelError{err: errors.New("upstream text")}, want: "model_call_failed"},
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
