package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

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
		},
		h.saveDashboard,
	)
}

type saveInput struct {
	ID   string `path:"id"`
	Body json.RawMessage
}

func (h *handler) saveDashboard(ctx context.Context, in *saveInput) (*dashboardOutput, error) {
	d, rev, err := parseBody(in.Body, true)
	if err != nil {
		return nil, err
	}
	result, err := h.service.SaveDashboard(
		ctx,
		dashboards.SaveDashboardParams{ID: dashboards.DashboardID(in.ID), ExpectedRevision: rev, Definition: d},
	)
	if err != nil {
		return nil, mapError(err)
	}
	return &dashboardOutput{Body: result}, nil
}
