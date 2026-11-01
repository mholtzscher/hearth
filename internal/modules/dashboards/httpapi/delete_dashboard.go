package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func registerDeleteDashboard(api huma.API, h *handler) {
	huma.Register(
		api,
		huma.Operation{
			OperationID:   "delete-dashboard",
			Method:        http.MethodDelete,
			Path:          dashboardPath,
			Summary:       "Delete a dashboard",
			DefaultStatus: http.StatusNoContent,
			Parameters: []*huma.Param{
				{
					Name:     "expected_revision",
					In:       "query",
					Required: true,
					Schema:   &huma.Schema{Type: "integer", Minimum: new(float64(1))},
				},
			},
			Errors: []int{
				http.StatusBadRequest,
				http.StatusNotFound,
				http.StatusConflict,
				http.StatusUnprocessableEntity,
				http.StatusInternalServerError,
			},
		},
		h.deleteDashboard,
	)
}

type deleteInput struct {
	ID       string `path:"id"`
	Revision string `          query:"expected_revision"`
}

func (h *handler) deleteDashboard(ctx context.Context, in *deleteInput) (*struct{}, error) {
	revision, err := strconv.ParseInt(in.Revision, 10, 64)
	if err != nil || revision < 1 {
		return nil, huma.Error400BadRequest("invalid revision")
	}
	if deleteErr := h.service.DeleteDashboard(ctx, dashboards.DashboardID(in.ID), revision); deleteErr != nil {
		return nil, h.mapError(ctx, "delete-dashboard", deleteErr)
	}
	return &struct{}{}, nil
}
