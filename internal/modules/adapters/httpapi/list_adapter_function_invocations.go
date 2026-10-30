//nolint:goconst // Literal operation metadata and schema fields keep the contract local.
package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerListAdapterFunctionInvocations(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-adapter-function-invocations",
		Method:      http.MethodGet,
		Path:        "/adapters/{adapter_id}/function-invocations",
		Summary:     "List retained function invocation evidence",
		Tags:        []string{"Adapters"},
		Responses: functionProblemResponses(api,
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusInternalServerError),
	}, h.listInvocations)
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
	return &huma.Schema{
		Type: "object", AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"items":       {Type: "array", Items: InvocationBody{}.Schema(nil)},
			"next_cursor": {Type: "string"},
		},
		Required: []string{"items"},
	}
}

func (h *handler) listInvocations(ctx context.Context, in *invocationListInput) (*invocationListOutput, error) {
	pageInput := adapters.PageInput{Limit: in.Limit, Cursor: in.Cursor}
	page, err := h.functions.ListInvocations(ctx, adapters.InvocationFilter{
		AdapterID: in.AdapterID,
		FunctionKey: adapters.FunctionKey(
			in.FunctionKey,
		),
		PageInput: pageInput,
	})
	if err != nil {
		return nil, h.mapFunctionError(ctx, "list-adapter-function-invocations", err)
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
