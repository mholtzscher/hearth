package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type ListAdaptersInput struct{ pageInput }
type AdapterPage struct {
	Items      []AdapterSummary `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func registerListAdapters(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[ListAdaptersInput, AdapterPage]{
		Name:        "list_adapters",
		Description: "List Adapter health summaries; inspect a selected Adapter for runtime evidence.",
		Handler:     h.listAdapters,
	})
}

func (h *handler) listAdapters(ctx context.Context, in ListAdaptersInput) (AdapterPage, error) {
	params := adapters.ListAdaptersParams{Limit: pageSize(in.Limit)}
	if in.Cursor != "" {
		position, err := apicursor.DecodeAdapters(in.Cursor)
		if err != nil {
			return AdapterPage{}, invalidRequest("invalid cursor")
		}
		params.AfterID = position
	}
	page, err := h.adapters.ListAdapters(ctx, params)
	if err != nil {
		return AdapterPage{}, managementFailure(err)
	}
	result := AdapterPage{Items: make([]AdapterSummary, len(page.Items))}
	for i, item := range page.Items {
		result.Items[i] = AdapterSummary{AdapterID: item.ID, Health: healthSummary(item.Health)}
	}
	if page.HasMore && len(page.Items) > 0 {
		result.NextCursor, err = apicursor.EncodeAdapters(page.Items[len(page.Items)-1].ID)
		if err != nil {
			return AdapterPage{}, mcptools.InternalToolError(err)
		}
	}
	return result, nil
}

type AdapterSummary struct {
	AdapterID string        `json:"adapter_id"`
	Health    HealthSummary `json:"health"`
}
