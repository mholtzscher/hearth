package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

type Presenter interface {
	PresentView(context.Context, dashboards.Definition) (dashboards.Artifact, error)
}

type handler struct{ presenter Presenter }
