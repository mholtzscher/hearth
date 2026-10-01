package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type PatchDeviceInput struct {
	DeviceID string          `path:"device_id" doc:"Canonical Hearth Device ID"`
	Body     PatchDeviceBody `                                                  contentType:"application/merge-patch+json"`
}
type PatchDeviceOutput struct{ Body DeviceBody }

func (handler *Handler) PatchDevice(ctx context.Context, input *PatchDeviceInput) (*PatchDeviceOutput, error) {
	id, err := devices.ParseDeviceID(input.DeviceID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
	}
	var device devices.Device
	if !input.Body.nameOverridePresent {
		var aggregate devices.DeviceAggregate
		aggregate, err = handler.devices.GetDevice(ctx, devices.GetDeviceParams{ID: id, EntityLimit: 1})
		device = aggregate.Device
	} else {
		device, err = handler.devices.PatchDevice(ctx, id, devices.DevicePatch{
			NameEdit: &devices.NameEdit{Override: input.Body.NameOverride},
		})
	}
	switch {
	case errors.Is(err, devices.ErrInvalidMetadataPatch):
		return nil, apiError(http.StatusBadRequest, "invalid metadata patch")
	case errors.Is(err, devices.ErrDeviceNotFound):
		return nil, apiError(http.StatusNotFound, "device not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	return &PatchDeviceOutput{Body: deviceBody(device)}, nil
}
