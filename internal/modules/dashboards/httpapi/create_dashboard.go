package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerCreateDashboard(api huma.API, h *handler) {
	huma.Register(
		api,
		huma.Operation{
			OperationID:      "create-dashboard",
			Method:           http.MethodPost,
			Path:             "/dashboards",
			Summary:          "Create a dashboard",
			DefaultStatus:    http.StatusCreated,
			SkipValidateBody: true,
			MaxBodyBytes:     requestMaxBytes,
			Errors: []int{
				http.StatusBadRequest,
				http.StatusRequestEntityTooLarge,
				http.StatusUnsupportedMediaType,
				http.StatusUnprocessableEntity,
				http.StatusInternalServerError,
			},
		},
		h.createDashboard,
	)
}

type createInput struct{ Body json.RawMessage }

func (h *handler) createDashboard(ctx context.Context, in *createInput) (*dashboardOutput, error) {
	d, _, err := h.parseBody(ctx, "create-dashboard", in.Body, false)
	if err != nil {
		return nil, err
	}
	result, err := h.service.CreateDashboard(ctx, d)
	if err != nil {
		return nil, h.mapError(ctx, "create-dashboard", err)
	}
	return &dashboardOutput{Body: result}, nil
}
