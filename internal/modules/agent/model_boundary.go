package agent

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// turnModel marks failures at the actual model call, not the enclosing graph.
// Turns use Generate only; nil output must be rejected here, before ReAct's
// tool-call checker dereferences it. WithTools keeps the bound model wrapped.
type turnModel struct {
	model.ToolCallingChatModel
}

func (boundary turnModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	response, err := boundary.ToolCallingChatModel.Generate(ctx, messages, options...)
	if err != nil {
		return nil, modelError{err: err}
	}
	if response == nil {
		return nil, modelError{err: errModelResponseInvalid}
	}
	return response, nil
}

func (boundary turnModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := boundary.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return turnModel{ToolCallingChatModel: bound}, nil
}
