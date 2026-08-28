package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ListEntitiesInput struct {
	Limit    int    `query:"limit"     default:"50" minimum:"1" maximum:"200"`
	Cursor   string `query:"cursor"`
	DeviceID string `query:"device_id"                                        doc:"Canonical Hearth Device ID"`
}

type ListEntitiesOutput struct {
	Body EntityCollectionBody
}

func (handler *Handler) ListEntities(ctx context.Context, input *ListEntitiesInput) (*ListEntitiesOutput, error) {
	var deviceID *devices.DeviceID
	if input.DeviceID != "" {
		parsed, err := devices.ParseDeviceID(input.DeviceID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
		}
		deviceID = &parsed
	}
	var afterID *devices.EntityID
	if input.Cursor != "" {
		parsed, err := decodeEntitiesCursor(input.Cursor, deviceID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: deviceID, AfterID: afterID, Limit: input.Limit,
	})
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, apiError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := EntityCollectionBody{Items: make([]EntityBody, len(page.Items))}
	for index, entity := range page.Items {
		mapped, err := entityBody(entity)
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, err := encodeEntitiesCursor(page.Items[len(page.Items)-1].Entity.ID, deviceID)
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListEntitiesOutput{Body: body}, nil
}
