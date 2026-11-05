// Package httpapi exposes explicit dashboard save operations.
package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

// Register publishes dashboard operations. A nil logger discards diagnostics.
func Register(api huma.API, service Dashboards, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handler{service: service, logger: logger}
	registerCreateDashboard(api, h)
	registerGetDashboard(api, h)
	registerListDashboards(api, h)
	registerSaveDashboard(api, h)
	registerDeleteDashboard(api, h)
}

const dashboardPath = "/dashboards/{id}"
const requestMaxBytes = 64 * 1024
const defaultPageLimit = 50
const maxPageLimit = 200
