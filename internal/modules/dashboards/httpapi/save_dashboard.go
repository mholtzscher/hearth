package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func registerSaveDashboard(api huma.API, h *handler) {
	huma.Register(
		api,
		huma.Operation{
			OperationID:      "save-dashboard",
			Method:           http.MethodPut,
			Path:             dashboardPath,
			Summary:          "Replace a dashboard",
			SkipValidateBody: true,
			MaxBodyBytes:     requestMaxBytes,
			RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
				"application/json": {
					Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[saveDashboardBody](), true, ""),
				},
			}},
			Errors: []int{
				http.StatusBadRequest,
				http.StatusNotFound,
				http.StatusConflict,
				http.StatusRequestEntityTooLarge,
				http.StatusUnsupportedMediaType,
				http.StatusUnprocessableEntity,
				http.StatusInternalServerError,
			},
		},
		h.saveDashboard,
	)
}

type saveInput struct {
	ID   string `path:"id"`
	Body json.RawMessage
}

type saveDashboardBody struct {
	ExpectedRevision int64              `json:"expected_revision" minimum:"1"`
	Definition       definitionContract `json:"definition"`
}

func (saveDashboardBody) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.AdditionalProperties = false
	return schema
}

func (h *handler) saveDashboard(ctx context.Context, in *saveInput) (*dashboardOutput, error) {
	d, rev, err := h.parseBody(ctx, "save-dashboard", in.Body, true)
	if err != nil {
		return nil, err
	}
	result, err := h.service.SaveDashboard(
		ctx,
		dashboards.SaveDashboardParams{ID: dashboards.DashboardID(in.ID), ExpectedRevision: rev, Definition: d},
	)
	if err != nil {
		return nil, h.mapError(ctx, "save-dashboard", err)
	}
	return &dashboardOutput{Body: result}, nil
}
