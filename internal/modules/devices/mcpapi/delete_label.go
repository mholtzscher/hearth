package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerDeleteLabel(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[DeleteLabelInput, DeleteLabelReceipt]{
			Name: "delete_label",
			Description: "Delete a household Label. Areas that name it are cleared and its tag " +
				"assignments are removed from every Device and Entity.",
			Handler: h.deleteLabel,
		},
	)
}

type DeleteLabelInput struct {
	LabelID labelID `json:"label_id" doc:"canonical Hearth Label ID"`
}

type DeleteLabelReceipt struct {
	LabelID string `json:"label_id"`
	Deleted bool   `json:"deleted"`
}

func (h *handler) deleteLabel(ctx context.Context, input DeleteLabelInput) (DeleteLabelReceipt, error) {
	if err := h.devices.DeleteLabel(ctx, devices.LabelID(input.LabelID)); err != nil {
		return DeleteLabelReceipt{}, labelFailure(err)
	}
	return DeleteLabelReceipt{LabelID: string(input.LabelID), Deleted: true}, nil
}
