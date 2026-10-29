package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListDevices(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-devices", Method: http.MethodGet, Path: "/devices",
		Summary: "List Devices", Tags: []string{devicesTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.listDevices)
}

type ListDevicesInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

type ListDevicesOutput struct {
	Body DeviceCollectionBody
}

func (handler *handler) listDevices(ctx context.Context, input *ListDevicesInput) (*ListDevicesOutput, error) {
	var afterID *devices.DeviceID
	if input.Cursor != "" {
		parsed, err := apicursor.DecodeDevicesCursor(input.Cursor)
		if err != nil {
			return nil, huma.NewError(http.StatusBadRequest, "invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListDevices(ctx, devices.ListDevicesParams{AfterID: afterID, Limit: input.Limit})
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, huma.NewError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "list-devices", "service", err)
	}
	body := DeviceCollectionBody{Items: make([]DeviceBody, len(page.Items))}
	for index, device := range page.Items {
		body.Items[index] = deviceBody(device)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeDevicesCursor(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return nil, handler.internalAPIError(ctx, "list-devices", "mapping", cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListDevicesOutput{Body: body}, nil
}

type DeviceCollectionBody struct {
	Items      []DeviceBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}
