package agent

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// turnModel marks failures at the actual model call, not the enclosing runner.
// Turns use Generate only; nil output must be rejected here, before ADK's
// tool-call checker dereferences it. ADK supplies tools through model options.
type turnModel struct {
	model.BaseChatModel
}

func (boundary turnModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	response, err := boundary.BaseChatModel.Generate(ctx, messages, options...)
	if err != nil {
		return nil, modelError{err: err}
	}
	if response == nil {
		return nil, modelError{err: errModelResponseInvalid}
	}
	return response, nil
}
