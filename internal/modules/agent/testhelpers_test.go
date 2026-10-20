package agent //nolint:testpackage // Tests construct the service with an injected fake model.

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeChatModel is a scripted ToolCallingChatModel. Generate signals started,
// then waits for release or context cancellation, so a test can hold one turn
// open and drive Drain deterministically.
type fakeChatModel struct {
	started chan struct{}
	release chan struct{}
	reply   string
	once    sync.Once
}

func newFakeChatModel(reply string) *fakeChatModel {
	return &fakeChatModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
		reply:   reply,
	}
}

func (fake *fakeChatModel) Generate(
	ctx context.Context,
	_ []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	fake.once.Do(func() { close(fake.started) })
	select {
	case <-fake.release:
		return schema.AssistantMessage(fake.reply, nil), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (fake *fakeChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fakeChatModel: Stream is not supported")
}

func (fake *fakeChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return fake, nil
}

// stubTool is a Tool.BaseTool that is never invoked: the scripted model returns
// a final answer without emitting tool calls.
type stubTool struct{ name string }

func (stub stubTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: stub.name, Desc: "test-only tool"}, nil
}

func (stub stubTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", nil
}

// failingChatModel fails every model call with a caller-supplied error, standing
// in for a provider or upstream rejection whose text must stay out of logs.
type failingChatModel struct{ err error }

func (model failingChatModel) Generate(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.Message, error) {
	return nil, model.err
}

func (model failingChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, model.err
}

func (model failingChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return model, nil
}
