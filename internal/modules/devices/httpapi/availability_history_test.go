package httpapi //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/pagination"
)

func TestEntityAvailabilityHistoryMapsEvidenceAndRejectsAdapterCursor(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	stub := &stubDevices{
		listEntityAvailabilityHistory: func(_ context.Context, params devices.ListEntityAvailabilityParams) (devices.Page[devices.HealthTransition], error) {
			if params.EntityID != apiEntityID || params.Limit != 50 || params.BeforeReceiveOrder != nil {
				t.Fatalf("Entity history params = %#v", params)
			}
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{{
				ReceiveOrder: 8, Status: "unavailable", Source: "adapter_health",
				Reason: &devices.HealthReason{Code: "hearth.network_unreachable"}, ObservedAt: observedAt,
			}}}, nil
		},
	}
	router, _ := testAPI(t, stub)
	response := performRequest(router, "/v1/entities/"+string(apiEntityID)+"/availability/history")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var history HealthTransitionCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].Status != "unavailable" ||
		history.Items[0].Source != "adapter_health" ||
		history.Items[0].SourceObservedAt != nil ||
		history.Items[0].ObservedAt != formatTime(observedAt) {
		t.Fatalf("Entity history = %#v", history)
	}
	// Existing Adapter cursors retain their old scope even after transport extraction.
	cursor, err := pagination.EncodeCursor(map[string]any{
		"v": 1, "resource": "adapter_health", "parent_id": apiAdapterID, "receive_order": 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/availability/history?cursor="+cursor)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("cross-route cursor status = %d", response.Code)
	}
}

func TestEntityAvailabilityHistoryMapsMissingAndInvalidParents(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{
		listEntityAvailabilityHistory: func(context.Context, devices.ListEntityAvailabilityParams) (devices.Page[devices.HealthTransition], error) {
			return devices.Page[devices.HealthTransition]{}, devices.ErrEntityNotFound
		},
	}
	router, _ := testAPI(t, stub)
	for _, test := range []struct {
		id     string
		status int
	}{{"bad", http.StatusBadRequest}, {string(apiEntityID), http.StatusNotFound}} {
		response := performRequest(router, "/v1/entities/"+test.id+"/availability/history")
		if response.Code != test.status {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	}
}
