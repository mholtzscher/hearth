package mcpapi

import (
	"context"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListLabels(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListLabelsInput, LabelList]{
			Name: "list_labels",
			Description: "List household Areas (rooms) and tags. Resolve a room or group name here, " +
				"then filter list_entities by label_ids.",
			Handler: h.listLabels,
		},
	)
}

type ListLabelsInput struct{}

type LabelList struct {
	Items []LabelDetail `json:"items" nullable:"false"`
}

func (h *handler) listLabels(ctx context.Context, _ ListLabelsInput) (LabelList, error) {
	labels, err := h.devices.ListLabels(ctx)
	if err != nil {
		return LabelList{}, mcptools.InternalToolError(err)
	}
	result := LabelList{Items: make([]LabelDetail, len(labels))}
	for index, label := range labels {
		result.Items[index] = labelDetail(label)
	}
	return result, nil
}
