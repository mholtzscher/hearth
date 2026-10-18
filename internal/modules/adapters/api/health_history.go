package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type ListAdapterHealthHistoryInput struct {
	AdapterID string `path:"adapter_id" doc:"Subject-safe Adapter ID"`
	Limit     int    `                                                query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor    string `                                                query:"cursor"`
}

type ListAdapterHealthHistoryOutput struct {
	Body AdapterHealthTransitionCollectionBody
}

func (handler *Handler) ListAdapterHealthHistory(
	ctx context.Context,
	input *ListAdapterHealthHistoryInput,
) (*ListAdapterHealthHistoryOutput, error) {
	if !validAdapterID(input.AdapterID) {
		return nil, apiError(http.StatusBadRequest, "adapter_id must be a subject-safe slug")
	}
	params := adapters.ListAdapterHealthParams{AdapterID: input.AdapterID, Limit: input.Limit}
	if input.Cursor != "" {
		before, err := decodeAdapterHealthCursor(input.Cursor, input.AdapterID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.adapters.ListAdapterHealthHistory(ctx, params)
	switch {
	case errors.Is(err, adapters.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return nil, apiError(http.StatusNotFound, "adapter not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	body := AdapterHealthTransitionCollectionBody{Items: make([]AdapterHealthTransitionBody, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeAdapterHealthCursor(input.AdapterID, page.Items[len(page.Items)-1].ReceiveOrder)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListAdapterHealthHistoryOutput{Body: body}, nil
}
