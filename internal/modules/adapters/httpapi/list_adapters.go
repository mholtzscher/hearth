package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/internal/apicursor"
)

func registerListAdapters(api huma.API, handler *handler) {
	const adaptersTag = "Adapters"
	huma.Register(api, huma.Operation{
		OperationID: "list-adapters", Method: http.MethodGet, Path: "/adapters",
		Summary: "List Adapters and their current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.listAdapters)
}

type ListAdaptersInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

type ListAdaptersOutput struct {
	Body AdapterCollectionBody
}

func (handler *handler) listAdapters(
	ctx context.Context,
	input *ListAdaptersInput,
) (*ListAdaptersOutput, error) {
	params := adapters.ListAdaptersParams{Limit: input.Limit}
	if input.Cursor != "" {
		afterID, err := apicursor.DecodeAdapters(input.Cursor)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.AfterID = afterID
	}
	page, err := handler.adapters.ListAdapters(ctx, params)
	if errors.Is(err, adapters.ErrInvalidPage) {
		return nil, apiError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, internalAPIError(err)
	}
	body := AdapterCollectionBody{Items: make([]AdapterBody, len(page.Items))}
	for index, adapter := range page.Items {
		body.Items[index] = adapterBody(adapter)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeAdapters(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListAdaptersOutput{Body: body}, nil
}

type AdapterCollectionBody struct {
	Items      []AdapterBody `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}
