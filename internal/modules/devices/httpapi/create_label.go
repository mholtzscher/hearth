package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerCreateLabel(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-label",
		Method:        http.MethodPost,
		Path:          "/labels",
		Summary:       "Create a Label",
		Tags:          []string{labelsTag},
		DefaultStatus: http.StatusCreated,
		Description:   "Creates an Area or tag. Names are unique across both kinds, ignoring case. A name conflict and the household Label limit both return 409 with code label_name_conflict or label_limit_reached.",
		Errors: []int{
			http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType,
			http.StatusUnprocessableEntity, http.StatusInternalServerError,
		},
		Responses: labelConflictResponses(api),
	}, handler.createLabel)
}

type CreateLabelInput struct {
	Body CreateLabelBody
}

type CreateLabelBody struct {
	Name string `json:"name" doc:"Display name, 1–128 code points"`
	Kind string `json:"kind" doc:"Fixed at creation"               enum:"area,tag"`
}

type CreateLabelOutput struct {
	Body LabelBody
}

func (handler *handler) createLabel(ctx context.Context, input *CreateLabelInput) (*CreateLabelOutput, error) {
	label, err := handler.devices.CreateLabel(ctx, devices.CreateLabelParams{
		Name: input.Body.Name, Kind: devices.LabelKind(input.Body.Kind),
	})
	if mapped := labelError(err); mapped != nil {
		return nil, mapped
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "create-label", "service", err)
	}
	return &CreateLabelOutput{Body: labelBody(label)}, nil
}
