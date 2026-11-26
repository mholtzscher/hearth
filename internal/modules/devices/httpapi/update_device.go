package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerUpdateDevice(api huma.API, handler *handler) {
	huma.Register(mergePatchAPI{api}, huma.Operation{
		OperationID: "update-device",
		Method:      http.MethodPatch,
		Path:        "/devices/{device_id}",
		Summary:     "Update a Device",
		Tags:        []string{devicesTag},
		Description: "Merge writable metadata fields. Omitted fields are unchanged; null name_override clears the override and null area_id clears the Area. tag_ids replaces the whole tag set and must not be null. An unknown or wrong-kind Label returns 400. An empty object returns the current Device without mutation.",
		Middlewares: huma.Middlewares{requireMergePatch(api)},
		Errors: []int{
			http.StatusBadRequest,
			http.StatusNotFound,
			http.StatusRequestEntityTooLarge,
			http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity,
			http.StatusInternalServerError,
		},
	}, handler.patchDevice)
}

type PatchDeviceInput struct {
	DeviceID string          `path:"device_id" doc:"Canonical Hearth Device ID"`
	Body     PatchDeviceBody `                                                  contentType:"application/merge-patch+json"`
}

type PatchDeviceOutput struct{ Body DeviceBody }

func (handler *handler) patchDevice(ctx context.Context, input *PatchDeviceInput) (*PatchDeviceOutput, error) {
	id, err := devices.ParseDeviceID(input.DeviceID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
	}
	areaEdit, tagEdit, err := labelPatchEdits(
		input.Body.areaPresent, input.Body.AreaID, input.Body.tagIDsPresent, input.Body.TagIDs,
	)
	if err != nil {
		return nil, err
	}
	var device devices.Device
	if !input.Body.nameOverridePresent && areaEdit == nil && tagEdit == nil {
		var aggregate devices.DeviceAggregate
		aggregate, err = handler.devices.GetDevice(ctx, devices.GetDeviceParams{ID: id, EntityLimit: 1})
		device = aggregate.Device
	} else {
		device, err = handler.devices.PatchDevice(ctx, id, devices.DevicePatch{
			NameEdit: domainNameEdit(input.Body.nameOverridePresent, input.Body.NameOverride),
			AreaEdit: areaEdit, TagEdit: tagEdit,
		})
	}
	switch {
	case errors.Is(err, devices.ErrInvalidMetadataPatch):
		return nil, huma.NewError(http.StatusBadRequest, "invalid metadata patch")
	case errors.Is(err, devices.ErrDeviceNotFound):
		return nil, huma.NewError(http.StatusNotFound, "device not found")
	case err != nil:
		return nil, handler.internalAPIError(ctx, "update-device", "service", err)
	}
	return &PatchDeviceOutput{Body: deviceBody(device)}, nil
}

type PatchDeviceBody struct {
	NameOverride        *string  `json:"name_override,omitempty" nullable:"true"`
	AreaID              *string  `json:"area_id,omitempty"       nullable:"true"  doc:"Area Label ID; null clears the Device's Area"`
	TagIDs              []string `json:"tag_ids,omitempty"       nullable:"false" doc:"Replaces the tag Label IDs assigned to the Device; an empty array clears them"`
	nameOverridePresent bool
	areaPresent         bool
	tagIDsPresent       bool
}

func (body *PatchDeviceBody) UnmarshalJSON(data []byte) error {
	properties, err := metadataPatchProperties(data, false)
	if err != nil {
		return err
	}
	type decodedBody PatchDeviceBody
	var decoded decodedBody
	if err = json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	_, decoded.nameOverridePresent = properties["name_override"]
	_, decoded.areaPresent = properties["area_id"]
	_, decoded.tagIDsPresent = properties["tag_ids"]
	*body = PatchDeviceBody(decoded)
	return nil
}
