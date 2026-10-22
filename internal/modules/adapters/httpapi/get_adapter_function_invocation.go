package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerGetAdapterFunctionInvocation(api huma.API, h *functionHandler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter-function-invocation",
		Method:      http.MethodGet,
		Path:        "/adapter-function-invocations/{invocation_id}",
		Summary:     "Read retained function invocation evidence",
		Tags:        []string{"Adapters"},
		Errors: []int{
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusServiceUnavailable, http.StatusInternalServerError,
		},
	}, h.getInvocation)
}

type invocationInput struct {
	InvocationID string `path:"invocation_id"`
}
type invocationOutput struct{ Body InvocationBody }

func (h *functionHandler) getInvocation(ctx context.Context, in *invocationInput) (*invocationOutput, error) {
	invocation, err := h.service.GetInvocation(ctx, adapters.InvocationID(in.InvocationID))
	if err != nil {
		return nil, functionHTTPError(err)
	}
	return &invocationOutput{Body: invocationBody(invocation)}, nil
}
