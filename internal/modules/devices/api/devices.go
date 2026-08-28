package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ListDevicesInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

type ListDevicesOutput struct {
	Body DeviceCollectionBody
}

type GetDeviceInput struct {
	DeviceID     string `path:"device_id" doc:"Canonical Hearth Device ID"`
	EntityLimit  int    `                                                  query:"entity_limit"  default:"50" minimum:"1" maximum:"200"`
	EntityCursor string `                                                  query:"entity_cursor"`
}

type GetDeviceOutput struct {
	Body DeviceDetailBody
}

func (handler *Handler) ListDevices(ctx context.Context, input *ListDevicesInput) (*ListDevicesOutput, error) {
	var afterID *devices.DeviceID
	if input.Cursor != "" {
		parsed, err := decodeDevicesCursor(input.Cursor)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListDevices(ctx, devices.ListDevicesParams{AfterID: afterID, Limit: input.Limit})
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, apiError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := DeviceCollectionBody{Items: make([]DeviceBody, len(page.Items))}
	for index, device := range page.Items {
		body.Items[index] = deviceBody(device)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, err := encodeDevicesCursor(page.Items[len(page.Items)-1].ID)
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListDevicesOutput{Body: body}, nil
}

func (handler *Handler) GetDevice(ctx context.Context, input *GetDeviceInput) (*GetDeviceOutput, error) {
	deviceID, err := devices.ParseDeviceID(input.DeviceID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
	}
	params := devices.GetDeviceParams{ID: deviceID, EntityLimit: input.EntityLimit}
	if input.EntityCursor != "" {
		afterID, err := decodeDeviceEntitiesCursor(input.EntityCursor, deviceID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid entity cursor")
		}
		params.AfterEntityID = afterID
	}
	aggregate, err := handler.devices.GetDevice(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrDeviceNotFound):
		return nil, apiError(http.StatusNotFound, "device not found")
	case err != nil:
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body, err := deviceDetailBody(aggregate)
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	if aggregate.Entities.HasMore && len(aggregate.Entities.Items) > 0 {
		cursor, err := encodeDeviceEntitiesCursor(
			aggregate.Entities.Items[len(aggregate.Entities.Items)-1].Entity.ID, deviceID,
		)
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextEntityCursor = &cursor
	}
	return &GetDeviceOutput{Body: body}, nil
}
