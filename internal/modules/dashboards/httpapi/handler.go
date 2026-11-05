package httpapi

import (
	"context"
	"log/slog"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

// Dashboards is the dashboard HTTP surface.
type Dashboards interface {
	CreateDashboard(context.Context, dashboards.Definition) (dashboards.Dashboard, error)
	GetDashboard(context.Context, dashboards.DashboardID) (dashboards.Dashboard, error)
	ListDashboards(context.Context, dashboards.ListParams) (dashboards.Page[dashboards.Summary], error)
	SaveDashboard(context.Context, dashboards.SaveDashboardParams) (dashboards.Dashboard, error)
	DeleteDashboard(context.Context, dashboards.DashboardID, int64) error
}
type handler struct {
	service Dashboards
	logger  *slog.Logger
}
