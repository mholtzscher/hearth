package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerListAdapterFunctionInvocations(api huma.API, h *functionHandler) {
	huma.Register(
		api,
		functionOperation(api,
			"list-adapter-function-invocations",
			http.MethodGet,
			"/adapters/{adapter_id}/function-invocations",
			"List retained function invocation evidence",
		),
		h.listInvocations,
	)
}

type invocationListInput struct {
	FunctionPageInput

	FunctionKey string `query:"function_key"`
}
type invocationListOutput struct{ Body InvocationCollectionBody }

type InvocationCollectionBody struct {
	Items      []InvocationBody `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

func (InvocationCollectionBody) Schema(_ huma.Registry) *huma.Schema {
	return functionCollectionSchema(InvocationBody{}.Schema(nil))
}

func (h *functionHandler) listInvocations(ctx context.Context, in *invocationListInput) (*invocationListOutput, error) {
	pageInput := adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor}
	page, err := h.service.ListInvocations(ctx, adapters.InvocationFilter{
		AdapterID: in.AdapterID,
		FunctionKey: adapters.FunctionKey(
			in.FunctionKey,
		),
		PageInput: pageInput,
	})
	if err != nil {
		return nil, functionHTTPError(err)
	}
	body := InvocationCollectionBody{
		Items:      make([]InvocationBody, len(page.Items)),
		NextCursor: functionNextCursor(page.NextCursor),
	}
	for index, item := range page.Items {
		body.Items[index] = invocationBody(item)
	}
	return &invocationListOutput{Body: body}, nil
}
