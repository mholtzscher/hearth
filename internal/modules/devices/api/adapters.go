package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

var adapterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type ListAdaptersInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

type ListAdaptersOutput struct {
	Body AdapterCollectionBody
}

type GetAdapterInput struct {
	AdapterID string `path:"adapter_id" doc:"Subject-safe Adapter ID"`
}

type GetAdapterOutput struct {
	Body AdapterBody
}

func (handler *Handler) ListAdapters(
	ctx context.Context,
	input *ListAdaptersInput,
) (*ListAdaptersOutput, error) {
	params := devices.ListAdaptersParams{Limit: input.Limit}
	if input.Cursor != "" {
		afterID, err := decodeAdaptersCursor(input.Cursor)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.AfterID = afterID
	}
	page, err := handler.devices.ListAdapters(ctx, params)
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, apiError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := AdapterCollectionBody{Items: make([]AdapterBody, len(page.Items))}
	for index, adapter := range page.Items {
		body.Items[index] = adapterBody(adapter)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeAdaptersCursor(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListAdaptersOutput{Body: body}, nil
}

func (handler *Handler) GetAdapter(ctx context.Context, input *GetAdapterInput) (*GetAdapterOutput, error) {
	if !validAdapterID(input.AdapterID) {
		return nil, apiError(http.StatusBadRequest, "adapter_id must be a subject-safe slug")
	}
	adapter, err := handler.devices.GetAdapter(ctx, input.AdapterID)
	switch {
	case errors.Is(err, devices.ErrAdapterNotFound):
		return nil, apiError(http.StatusNotFound, "adapter not found")
	case err != nil:
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &GetAdapterOutput{Body: adapterBody(adapter)}, nil
}

func validAdapterID(value string) bool {
	return adapterIDPattern.MatchString(value)
}
