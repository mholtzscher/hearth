package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ListAdapterHealthHistoryInput struct {
	AdapterID string `path:"adapter_id" doc:"Subject-safe Adapter ID"`
	Limit     int    `                                                query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor    string `                                                query:"cursor"`
}

type ListAdapterHealthHistoryOutput struct {
	Body HealthTransitionCollectionBody
}

type ListEntityAvailabilityHistoryInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

type ListEntityAvailabilityHistoryOutput struct {
	Body HealthTransitionCollectionBody
}

func (handler *Handler) ListAdapterHealthHistory(
	ctx context.Context,
	input *ListAdapterHealthHistoryInput,
) (*ListAdapterHealthHistoryOutput, error) {
	if !validAdapterID(input.AdapterID) {
		return nil, apiError(http.StatusBadRequest, "adapter_id must be a subject-safe slug")
	}
	params := devices.ListAdapterHealthParams{AdapterID: input.AdapterID, Limit: input.Limit}
	if input.Cursor != "" {
		before, err := decodeAdapterHealthCursor(input.Cursor, input.AdapterID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListAdapterHealthHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrAdapterNotFound):
		return nil, apiError(http.StatusNotFound, "adapter not found")
	case err != nil:
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := HealthTransitionCollectionBody{Items: make([]HealthTransitionBody, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeAdapterHealthCursor(
			input.AdapterID,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListAdapterHealthHistoryOutput{Body: body}, nil
}

func (handler *Handler) ListEntityAvailabilityHistory(
	ctx context.Context,
	input *ListEntityAvailabilityHistoryInput,
) (*ListEntityAvailabilityHistoryOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params := devices.ListEntityAvailabilityParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor != "" {
		before, cursorErr := decodeEntityAvailabilityCursor(input.Cursor, entityID)
		if cursorErr != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityAvailabilityHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, apiError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := HealthTransitionCollectionBody{Items: make([]HealthTransitionBody, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeEntityAvailabilityCursor(
			entityID,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListEntityAvailabilityHistoryOutput{Body: body}, nil
}
