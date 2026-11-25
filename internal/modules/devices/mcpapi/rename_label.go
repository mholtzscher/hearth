package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerRenameLabel(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[RenameLabelInput, LabelDetail]{
			Name:        "rename_label",
			Description: "Rename a household Label. Its kind and assignments are unchanged.",
			Handler:     h.renameLabel,
		},
	)
}

type RenameLabelInput struct {
	LabelID labelID `json:"label_id" doc:"canonical Hearth Label ID"`
	Name    string  `json:"name"     doc:"display name of 1 to 128 code points after trimming"`
}

func (h *handler) renameLabel(ctx context.Context, input RenameLabelInput) (LabelDetail, error) {
	label, err := h.devices.RenameLabel(ctx, devices.LabelID(input.LabelID), input.Name)
	if err != nil {
		return LabelDetail{}, labelFailure(err)
	}
	return labelDetail(label), nil
}
