package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

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
			RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
				"application/json": {
					Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[createDashboardBody](), true, ""),
				},
			}},
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

type createDashboardBody struct {
	Definition definitionContract `json:"definition"`
}

func (createDashboardBody) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.AdditionalProperties = false
	return schema
}

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
