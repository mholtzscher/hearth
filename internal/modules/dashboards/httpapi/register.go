// Package httpapi exposes explicit dashboard save operations.
package httpapi

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func Register(api huma.API, service *dashboards.Service) {
	h := &handler{service: service}
	registerCreateDashboard(api, h)
	registerGetDashboard(api, h)
	registerListDashboards(api, h)
	registerSaveDashboard(api, h)
	registerDeleteDashboard(api, h)
}

type handler struct{ service *dashboards.Service }

const dashboardPath = "/dashboards/{id}"
const requestMaxBytes = 64*1024 + 1
