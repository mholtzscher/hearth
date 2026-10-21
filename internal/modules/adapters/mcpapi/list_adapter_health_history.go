package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type ListAdapterHealthHistoryInput struct {
	pageInput

	AdapterID adapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}
type HealthTransition struct {
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	ObservedAt       string  `json:"observed_at"`
	SourceObservedAt *string `json:"source_observed_at,omitempty"`
	ReasonCode       string  `json:"reason_code,omitempty"`
}
type HealthPage struct {
	Items      []HealthTransition `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func registerListAdapterHealthHistory(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[ListAdapterHealthHistoryInput, HealthPage]{
		Name: "list_adapter_health_history", Description: "Read paginated diagnostic Adapter health transitions.",
		Handler: h.listAdapterHealthHistory,
	})
}

func (h *handler) listAdapterHealthHistory(ctx context.Context, in ListAdapterHealthHistoryInput) (HealthPage, error) {
	params := adapters.ListAdapterHealthParams{AdapterID: string(in.AdapterID), Limit: pageSize(in.Limit)}
	if in.Cursor != "" {
		position, err := apicursor.DecodeHealth(in.Cursor, params.AdapterID)
		if err != nil {
			return HealthPage{}, invalidRequest("invalid cursor")
		}
		params.BeforeReceiveOrder = position
	}
	page, err := h.adapters.ListAdapterHealthHistory(ctx, params)
	if err != nil {
		return HealthPage{}, managementFailure(err)
	}
	result := HealthPage{Items: make([]HealthTransition, len(page.Items))}
	for i, item := range page.Items {
		entry := HealthTransition{Status: item.Status, Source: item.Source, ObservedAt: formatTime(item.ObservedAt)}
		if item.Reason != nil {
			entry.ReasonCode = item.Reason.Code
		}
		if item.SourceObservedAt != nil {
			value := formatTime(*item.SourceObservedAt)
			entry.SourceObservedAt = &value
		}
		result.Items[i] = entry
	}
	if page.HasMore && len(page.Items) > 0 {
		result.NextCursor, err = apicursor.EncodeHealth(params.AdapterID, page.Items[len(page.Items)-1].ReceiveOrder)
		if err != nil {
			return HealthPage{}, mcptools.InternalToolError(err)
		}
	}
	return result, nil
}
