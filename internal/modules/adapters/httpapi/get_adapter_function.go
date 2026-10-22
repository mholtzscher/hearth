package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerGetAdapterFunction(api huma.API, h *functionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter-function",
		Method:      http.MethodGet,
		Path:        "/adapters/{adapter_id}/functions/{function_key}",
		Summary:     "Describe an Adapter function before invocation",
		Tags:        []string{"Adapters"},
		Errors: []int{
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusServiceUnavailable, http.StatusInternalServerError,
		},
	}, h.getFunction)
}

type functionOutput struct{ Body FunctionBody }

func (h *functionHandler) getFunction(ctx context.Context, in *FunctionInput) (*functionOutput, error) {
	function, err := h.service.GetFunction(ctx, in.AdapterID, adapters.FunctionKey(in.FunctionKey))
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &functionOutput{Body: functionBody(function)}, nil
}
