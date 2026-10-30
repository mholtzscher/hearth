package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerGetAdapterFunction(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter-function",
		Method:      http.MethodGet,
		Path:        "/adapters/{adapter_id}/functions/{function_key}",
		Summary:     "Describe an Adapter function before invocation",
		Tags:        []string{"Adapters"},
		Responses: functionProblemResponses(api,
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusInternalServerError),
	}, h.getFunction)
}

type functionOutput struct{ Body FunctionBody }

func (h *handler) getFunction(ctx context.Context, in *FunctionInput) (*functionOutput, error) {
	function, err := h.functions.GetFunction(ctx, in.AdapterID, adapters.FunctionKey(in.FunctionKey))
	if err != nil {
		return nil, h.mapFunctionError(ctx, "get-adapter-function", err)
	}
	return &functionOutput{Body: functionBody(function)}, nil
}
