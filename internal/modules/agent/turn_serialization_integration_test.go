//go:build integration

package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSendMessageSerializesTurnsPerConversation protects the per-conversation
// turn contract: two clients submitting at once must not overlap, because both
// would rebuild the same history snapshot and interleave their rows. It fails
// if the second turn reaches the model while the first is still running, or if
// the second turn's input misses the first turn's persisted reply.
func TestSendMessageSerializesTurnsPerConversation(t *testing.T) {
	t.Parallel()
	chatModel := newRecordingChatModel("first reply")
	service := newTurnService(t, chatModel)
	conversation := mustCreateConversation(t, service)
	ctx := context.Background()

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, conversation.ID, "first message")
		firstDone <- err
	}()
	select {
	case <-chatModel.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn never reached the model")
	}

	secondSubmitted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondSubmitted)
		_, err := service.SendMessage(ctx, conversation.ID, "second message")
		secondDone <- err
	}()
	<-secondSubmitted

	// The second turn must wait for the conversation rather than start its own
	// model call next to the running one.
	overlapped := false
	select {
	case <-chatModel.entered:
		overlapped = true
	case <-time.After(serializationGrace):
	}
	chatModel.allowAll()

	for name, done := range map[string]chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s turn: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s turn did not finish", name)
		}
	}
	if overlapped {
		t.Error("the second turn reached the model while the first turn was running")
	}
	if peak := chatModel.peakConcurrency(); peak != 1 {
		t.Errorf("peak model concurrency = %d, want 1", peak)
	}

	// The second turn rebuilt history from the first turn's persisted rows.
	tail := lastRolesAndContent(t, chatModel.lastInput(t), 3)
	want := []string{"user:first message", "assistant:first reply", "user:second message"}
	if strings.Join(tail, "|") != strings.Join(want, "|") {
		t.Fatalf("second turn input tail = %v, want %v", tail, want)
	}
}
