package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerCreateLabel(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[CreateLabelInput, LabelDetail]{
			Name: "create_label",
			Description: "Create a household Label: an area (a room; a Device or Entity has at most one) or a " +
				"tag (a free-form group). Names are unique across both kinds, ignoring case.",
			Handler: h.createLabel,
		},
	)
}

type CreateLabelInput struct {
	Name string `json:"name" doc:"display name of 1 to 128 code points after trimming"`
	Kind string `json:"kind" doc:"area for a room, tag for a group"                    enum:"area,tag"`
}

func (h *handler) createLabel(ctx context.Context, input CreateLabelInput) (LabelDetail, error) {
	label, err := h.devices.CreateLabel(ctx, devices.CreateLabelParams{
		Name: input.Name, Kind: devices.LabelKind(input.Kind),
	})
	if err != nil {
		return LabelDetail{}, labelFailure(err)
	}
	return labelDetail(label), nil
}
