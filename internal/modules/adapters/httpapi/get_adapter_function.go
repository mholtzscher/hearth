package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerGetAdapterFunction(api huma.API, h *functionHandler) {
	huma.Register(
		api,
		functionOperation(api,
			"get-adapter-function",
			http.MethodGet,
			"/adapters/{adapter_id}/functions/{function_key}",
			"Describe an Adapter function before invocation",
		),
		h.getFunction,
	)
}

type functionOutput struct{ Body FunctionBody }

func (h *functionHandler) getFunction(ctx context.Context, in *FunctionInput) (*functionOutput, error) {
	function, err := h.service.GetFunction(ctx, in.AdapterID, adapters.FunctionKey(in.FunctionKey))
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &functionOutput{Body: functionBody(function)}, nil
}
