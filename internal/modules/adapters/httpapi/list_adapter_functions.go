//nolint:goconst // Literal operation metadata and schema fields keep the contract local.
package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerListAdapterFunctions(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-adapter-functions",
		Method:      http.MethodGet,
		Path:        "/adapters/{adapter_id}/functions",
		Summary:     "Discover Adapter functions",
		Tags:        []string{"Adapters"},
		Errors: []int{
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusServiceUnavailable, http.StatusInternalServerError,
		},
	}, h.listFunctions)
}

type functionListOutput struct{ Body FunctionCollectionBody }

type FunctionCollectionBody struct {
	Items      []FunctionBody `json:"items"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

func (FunctionCollectionBody) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type: "object", AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"items":       {Type: "array", Items: FunctionBody{}.Schema(nil)},
			"next_cursor": {Type: "string"},
		},
		Required: []string{"items"},
	}
}

func (h *handler) listFunctions(ctx context.Context, in *FunctionPageInput) (*functionListOutput, error) {
	page, err := h.functions.ListFunctions(ctx, in.AdapterID, adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, h.mapFunctionError(ctx, "list-adapter-functions", err)
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
