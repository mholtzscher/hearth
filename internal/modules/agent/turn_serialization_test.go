package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// serializationGrace is how long the serialization test watches for a second
// turn reaching the model while the first turn still holds the conversation.
const serializationGrace = 500 * time.Millisecond

// recordingChatModel records every model call's input, signals each call as it
// starts, and counts how many calls run at once. It holds each call until
// release closes, so a test can keep one turn inside the model.
type recordingChatModel struct {
	mu       sync.Mutex
	inputs   [][]*schema.Message
	active   int
	peak     int
	entered  chan struct{}
	release  chan struct{}
	reply    string
	released sync.Once
}

func newRecordingChatModel(reply string) *recordingChatModel {
	return &recordingChatModel{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
		reply:   reply,
	}
}

func (model *recordingChatModel) Generate(
	ctx context.Context,
	input []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	model.mu.Lock()
	model.active++
	if model.active > model.peak {
		model.peak = model.active
	}
	model.inputs = append(model.inputs, append([]*schema.Message(nil), input...))
	model.mu.Unlock()
	defer func() {
		model.mu.Lock()
		model.active--
		model.mu.Unlock()
	}()

	model.entered <- struct{}{}
	select {
	case <-model.release:
		return schema.AssistantMessage(model.reply, nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (model *recordingChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, context.Canceled
}

func (model *recordingChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return model, nil
}

// allowAll releases every held model call exactly once.
func (model *recordingChatModel) allowAll() {
	model.released.Do(func() { close(model.release) })
}

// peakConcurrency reports the largest number of model calls that ran at once.
func (model *recordingChatModel) peakConcurrency() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.peak
}

// lastInput returns the newest recorded model input.
func (model *recordingChatModel) lastInput(t *testing.T) []*schema.Message {
	t.Helper()
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.inputs) == 0 {
		t.Fatal("no model call was recorded")
	}
	return model.inputs[len(model.inputs)-1]
}

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

// lastRolesAndContent projects the newest size messages to role:content pairs,
// ignoring the system prompt and any earlier history.
func lastRolesAndContent(t *testing.T, messages []*schema.Message, size int) []string {
	t.Helper()
	if len(messages) < size {
		t.Fatalf("model input has %d messages, want at least %d", len(messages), size)
	}
	projected := make([]string, 0, size)
	for _, message := range messages[len(messages)-size:] {
		projected = append(projected, string(message.Role)+":"+message.Content)
	}
	return projected
}
