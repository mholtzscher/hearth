package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerGetDevice(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-device", Method: http.MethodGet, Path: "/devices/{device_id}",
		Summary: "Get a Device and its Entities", Tags: []string{devicesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.getDevice)
}

type GetDeviceInput struct {
	DeviceID     string `path:"device_id" doc:"Canonical Hearth Device ID"`
	EntityLimit  int    `                                                  query:"entity_limit"  default:"50" minimum:"1" maximum:"200"`
	EntityCursor string `                                                  query:"entity_cursor"`
}

type GetDeviceOutput struct {
	Body DeviceDetailBody
}

func (handler *handler) getDevice(ctx context.Context, input *GetDeviceInput) (*GetDeviceOutput, error) {
	deviceID, err := devices.ParseDeviceID(input.DeviceID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
	}
	params := devices.GetDeviceParams{ID: deviceID, EntityLimit: input.EntityLimit}
	if input.EntityCursor != "" {
		afterID, cursorErr := apicursor.DecodeDeviceEntitiesCursor(input.EntityCursor, deviceID)
		if cursorErr != nil {
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
		return nil, internalAPIError(err)
	}
	body, err := deviceDetailBody(aggregate)
	if err != nil {
		return nil, internalAPIError(err)
	}
	if aggregate.Entities.HasMore && len(aggregate.Entities.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeDeviceEntitiesCursor(
			aggregate.Entities.Items[len(aggregate.Entities.Items)-1].Entity.ID, deviceID,
		)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextEntityCursor = &cursor
	}
	return &GetDeviceOutput{Body: body}, nil
}

type DeviceDetailBody struct {
	AdapterName      string       `json:"adapter_name"`
	NameOverride     *string      `json:"name_override"                nullable:"true"`
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	Name             string       `json:"name"`
	Entities         []EntityBody `json:"entities"`
	NextEntityCursor *string      `json:"next_entity_cursor,omitempty"`
}

func deviceDetailBody(aggregate devices.DeviceAggregate) (DeviceDetailBody, error) {
	body := DeviceDetailBody{
		AdapterName: aggregate.Device.AdapterName, NameOverride: aggregate.Device.NameOverride,
		ID: string(aggregate.Device.ID), Kind: string(aggregate.Device.Kind), Name: aggregate.Device.Name,
		Entities: make([]EntityBody, len(aggregate.Entities.Items)),
	}
	for index, entity := range aggregate.Entities.Items {
		mapped, err := entityBody(entity)
		if err != nil {
			return DeviceDetailBody{}, err
		}
		body.Entities[index] = mapped
	}
	return body, nil
}
