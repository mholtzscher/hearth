package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerListAdapterFunctions(api huma.API, h *functionHandler) {
	huma.Register(
		api,
		functionOperation(api,
			"list-adapter-functions",
			http.MethodGet,
			"/adapters/{adapter_id}/functions",
			"Discover Adapter functions",
		),
		h.listFunctions,
	)
}

type functionListOutput struct{ Body FunctionCollectionBody }

type FunctionCollectionBody struct {
	Items      []FunctionBody `json:"items"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

func (FunctionCollectionBody) Schema(_ huma.Registry) *huma.Schema {
	return functionCollectionSchema(FunctionBody{}.Schema(nil))
}

func (h *functionHandler) listFunctions(ctx context.Context, in *FunctionPageInput) (*functionListOutput, error) {
	page, err := h.service.ListFunctions(ctx, in.AdapterID, adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, functionHTTPError(err)
	}
	body := FunctionCollectionBody{
		Items:      make([]FunctionBody, len(page.Items)),
		NextCursor: functionNextCursor(page.NextCursor),
	}
	for index, item := range page.Items {
		body.Items[index] = functionBody(item)
	}
	return &functionListOutput{Body: body}, nil
}
