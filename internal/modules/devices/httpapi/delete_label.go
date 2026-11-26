package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerDeleteLabel(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-label", Method: http.MethodDelete, Path: "/labels/{label_id}",
		Summary: "Delete a Label", Tags: []string{labelsTag}, DefaultStatus: http.StatusNoContent,
		Description: "Deletes a Label, clearing Areas that name it and removing its tag assignments.",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.deleteLabel)
}

type DeleteLabelInput struct {
	LabelID string `path:"label_id" doc:"Canonical Hearth Label ID"`
}

func (handler *handler) deleteLabel(ctx context.Context, input *DeleteLabelInput) (*struct{}, error) {
	id, err := devices.ParseLabelID(input.LabelID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "label_id must be a canonical Hearth Label ID")
	}
	err = handler.devices.DeleteLabel(ctx, id)
	if mapped := labelError(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "delete-label", "service", err)
	}
	return nil, nil //nolint:nilnil // A 204 response has no body.
}
