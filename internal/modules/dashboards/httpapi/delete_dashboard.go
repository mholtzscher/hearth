package httpapi

import (
	"context"
	"net/http"

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
		},
		h.deleteDashboard,
	)
}

type deleteInput struct {
	ID       string `path:"id"`
	Revision int64  `          query:"expected_revision" required:"true"`
}

func (h *handler) deleteDashboard(ctx context.Context, in *deleteInput) (*struct{}, error) {
	if err := h.service.DeleteDashboard(ctx, dashboards.DashboardID(in.ID), in.Revision); err != nil {
		return nil, mapError(err)
	}
	return &struct{}{}, nil
}
