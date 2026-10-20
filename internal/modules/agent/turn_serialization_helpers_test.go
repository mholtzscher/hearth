package agent //nolint:testpackage // Tests drive the real service with a fake model.

import (
	"context"
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
