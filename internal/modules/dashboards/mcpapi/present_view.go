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
			Name: "present_view",
			Description: "Present a portable live view without saving a dashboard. " +
				"A view references at most 16 distinct Entities and Devices. " +
				"A TimeSeries charts distinct Entities of one entity_type and field over one window and unit. " +
				"A rejection names the violated rule; correct it before presenting again.",
			Handler: h.presentView,
		},
	)
}

func (h *handler) presentView(ctx context.Context, in presentInput) (dashboards.Artifact, error) {
	artifact, err := h.presenter.PresentView(ctx, in.Definition.value)
	if errors.Is(err, dashboards.ErrInvalidDefinition) {
		return dashboards.Artifact{}, invalidDefinitionError(err)
	}
	if err != nil {
		return dashboards.Artifact{}, mcptools.InternalToolError(err)
	}
	return artifact, nil
}

// invalidDefinitionError names the violated rule when the definition has one;
// its reason is fixed text, so the result never quotes submitted values.
func invalidDefinitionError(err error) *mcptools.ToolError {
	message := dashboards.ErrInvalidDefinition.Error()
	if rejected, ok := errors.AsType[dashboards.DefinitionError](err); ok {
		message = rejected.Error()
	}
	return &mcptools.ToolError{Code: "invalid_definition", Message: message}
}

type presentInput struct {
	Definition definitionValue `json:"definition"`
}
type definitionValue struct{ value dashboards.Definition }

func (v *definitionValue) UnmarshalJSON(raw []byte) error {
	d, err := dashboards.ParseDefinition(raw)
	if err != nil {
		return invalidDefinitionError(err)
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
