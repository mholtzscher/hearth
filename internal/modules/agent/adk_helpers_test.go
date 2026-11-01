package agent //nolint:testpackage // Shared scripted model and tool fixtures.

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type scriptedTurnModel struct {
	failingChatModel

	generate func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error)
}

func (script *scriptedTurnModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	return script.generate(ctx, messages, options...)
}

func (script *scriptedTurnModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return script, nil
}

type callbackTool struct {
	name   string
	invoke func(context.Context, string) (string, error)
}

func (callback callbackTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: callback.name}, nil
}

func (callback callbackTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	return callback.invoke(ctx, arguments)
}

func nativeTestCall(id, name, arguments string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}}
}
