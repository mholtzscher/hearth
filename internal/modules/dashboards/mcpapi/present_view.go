package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerPresentView(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[presentInput, dashboards.Artifact]{
			Name:        "present_view",
			Description: "Present a portable live view without saving a dashboard.",
			Handler:     h.presentView,
		},
	)
}

func (h *handler) presentView(ctx context.Context, in presentInput) (dashboards.Artifact, error) {
	artifact, err := h.presenter.PresentView(ctx, in.Definition.value)
	if errors.Is(err, dashboards.ErrInvalidDefinition) {
		return dashboards.Artifact{}, &mcptools.ToolError{
			Code:    "invalid_definition",
			Message: "invalid view definition",
		}
	}
	if err != nil {
		return dashboards.Artifact{}, mcptools.InternalToolError(err)
	}
	return artifact, nil
}

type presentInput struct {
	Definition definitionValue `json:"definition"`
}
type definitionValue struct{ value dashboards.Definition }

func (v *definitionValue) UnmarshalJSON(raw []byte) error {
	d, err := dashboards.ParseDefinition(raw)
	if err != nil {
		return &mcptools.ToolError{Code: "invalid_definition", Message: "invalid view definition"}
	}
	v.value = d
	return nil
}
func (definitionValue) Schema(_ huma.Registry) *huma.Schema {
	var document map[string]any
	if err := json.Unmarshal(viewsv1.Schema, &document); err != nil {
		panic(err)
	}
	return &huma.Schema{Extensions: document}
}
