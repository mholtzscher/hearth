package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func registerGetDashboard(api huma.API, h *handler) {
	huma.Register(
		api,
		huma.Operation{
			OperationID: "get-dashboard",
			Method:      http.MethodGet,
			Path:        dashboardPath,
			Summary:     "Get a dashboard",
			Errors: []int{
				http.StatusBadRequest,
				http.StatusNotFound,
				http.StatusUnprocessableEntity,
				http.StatusInternalServerError,
			},
		},
		h.getDashboard,
	)
}

type getInput struct {
	ID string `path:"id"`
}

func (h *handler) getDashboard(ctx context.Context, in *getInput) (*dashboardOutput, error) {
	result, err := h.service.GetDashboard(ctx, dashboards.DashboardID(in.ID))
	if err != nil {
		return nil, h.mapError(ctx, "get-dashboard", err)
	}
	return &dashboardOutput{Body: result}, nil
}
