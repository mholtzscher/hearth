package dashboards

import "context"

type Repository interface {
	CreateDashboard(context.Context, Definition) (Dashboard, error)
	GetDashboard(context.Context, DashboardID) (Dashboard, error)
	ListDashboards(context.Context, ListParams) (Page[Summary], error)
	SaveDashboard(context.Context, SaveDashboardParams) (Dashboard, error)
	DeleteDashboard(context.Context, DashboardID, int64) error
}
