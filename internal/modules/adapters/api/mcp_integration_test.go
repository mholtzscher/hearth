//go:build integration

package api //nolint:testpackage // MCP request mapping is owned by this transport.

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestMCPAdapterToolsTranslateToServiceCalls(t *testing.T) {
	t.Parallel()
	var adaptersParams adapters.ListAdaptersParams
	var adapterID string
	var healthParams adapters.ListAdapterHealthParams
	stub := &stubAdapters{
		listAdapters: func(_ context.Context, params adapters.ListAdaptersParams) (adapters.Page[adapters.AdapterInstance], error) {
			adaptersParams = params
			return adapters.Page[adapters.AdapterInstance]{
				Items: []adapters.AdapterInstance{
					{ID: apiAdapterID, Health: adapters.AdapterHealth{Status: "healthy", Source: "adapter"}},
				},
			}, nil
		},
		getAdapter: func(_ context.Context, requested string) (adapters.AdapterInstance, error) {
			adapterID = requested
			return adapters.AdapterInstance{
				ID:     apiAdapterID,
				Health: adapters.AdapterHealth{Status: "healthy", Source: "adapter"},
			}, nil
		},
		listAdapterHealthHistory: func(_ context.Context, params adapters.ListAdapterHealthParams) (adapters.Page[adapters.HealthTransition], error) {
			healthParams = params
			return adapters.Page[adapters.HealthTransition]{
				Items: []adapters.HealthTransition{
					{
						Status:       "healthy",
						Source:       "adapter",
						ObservedAt:   time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC),
						ReceiveOrder: 3,
					},
				},
			}, nil
		},
	}
	session := mcpSession(t, stub)
	adapterPage := mcpCallTool(t, session, "list_adapters", map[string]any{"limit": 2})
	var page AdapterCollectionBody
	mcpBody(t, adapterPage, &page)
	if adaptersParams.Limit != 2 || len(page.Items) != 1 || page.Items[0].ID != apiAdapterID {
		t.Fatalf("ListAdapters params = %#v, body = %#v", adaptersParams, page)
	}
	adapter := mcpCallTool(t, session, "get_adapter", map[string]any{"adapter_id": apiAdapterID})
	var body AdapterBody
	mcpBody(t, adapter, &body)
	if adapterID != apiAdapterID || body.ID != apiAdapterID || body.Health.Status != "healthy" {
		t.Fatalf("GetAdapter ID = %q, body = %#v", adapterID, body)
	}
	healthPage := mcpCallTool(
		t,
		session,
		"list_adapter_health_history",
		map[string]any{"adapter_id": apiAdapterID, "limit": 4},
	)
	var health AdapterHealthTransitionCollectionBody
	mcpBody(t, healthPage, &health)
	if healthParams.AdapterID != apiAdapterID || healthParams.Limit != 4 || healthParams.BeforeReceiveOrder != nil ||
		len(health.Items) != 1 {
		t.Fatalf("ListAdapterHealthHistory params = %#v, body = %#v", healthParams, health)
	}
}
