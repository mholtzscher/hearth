package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerUpdateLabel(api huma.API, handler *handler) {
	huma.Register(mergePatchAPI{api}, huma.Operation{
		OperationID: "update-label",
		Method:      http.MethodPatch,
		Path:        "/labels/{label_id}",
		Summary:     "Rename a Label",
		Tags:        []string{labelsTag},
		Description: "Merge writable fields. Only name is editable and it must not be null; kind never changes. An empty object returns the current Label without mutation.",
		Middlewares: huma.Middlewares{requireMergePatch(api)},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge,
			http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusInternalServerError,
		},
		Responses: labelConflictResponses(api),
	}, handler.patchLabel)
}

type PatchLabelInput struct {
	LabelID string         `path:"label_id" doc:"Canonical Hearth Label ID"`
	Body    PatchLabelBody `                                                contentType:"application/merge-patch+json"`
}

type PatchLabelOutput struct {
	Body LabelBody
}

type PatchLabelBody struct {
	Name *string `json:"name,omitempty"`
}

func (body *PatchLabelBody) UnmarshalJSON(data []byte) error {
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return err
	}
	if properties == nil {
		return &huma.ErrorDetail{Location: "body", Message: "patch must be an object"}
	}
	for key, value := range properties {
		switch {
		case key != "name":
			return &huma.ErrorDetail{Location: "body." + key, Message: "property is not editable"}
		case bytes.Equal(bytes.TrimSpace(value), []byte("null")):
			return &huma.ErrorDetail{Location: "body.name", Message: "name must not be null"}
		}
	}
	type decodedBody PatchLabelBody
	var decoded decodedBody
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*body = PatchLabelBody(decoded)
	return nil
}

func (handler *handler) patchLabel(ctx context.Context, input *PatchLabelInput) (*PatchLabelOutput, error) {
	id, err := devices.ParseLabelID(input.LabelID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "label_id must be a canonical Hearth Label ID")
	}
	var label devices.Label
	if input.Body.Name == nil {
		label, err = handler.findLabel(ctx, id)
	} else {
		label, err = handler.devices.RenameLabel(ctx, id, *input.Body.Name)
	}
	if mapped := labelError(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "update-label", "service", err)
	}
	return &PatchLabelOutput{Body: labelBody(label)}, nil
}

// findLabel reads one Label from the bounded catalog.
func (handler *handler) findLabel(ctx context.Context, id devices.LabelID) (devices.Label, error) {
	labels, err := handler.devices.ListLabels(ctx)
	if err != nil {
		return devices.Label{}, err
	}
	for _, label := range labels {
		if label.ID == id {
			return label, nil
		}
	}
	return devices.Label{}, devices.ErrLabelNotFound
}
