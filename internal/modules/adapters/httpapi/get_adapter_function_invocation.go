package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerGetAdapterFunctionInvocation(api huma.API, h *functionHandler) {
	huma.Register(
		api,
		functionOperation(api,
			"get-adapter-function-invocation",
			http.MethodGet,
			"/adapter-function-invocations/{invocation_id}",
			"Read retained function invocation evidence",
		),
		h.getInvocation,
	)
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
