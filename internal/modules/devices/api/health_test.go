package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const apiAdapterID = "simulator"

//nolint:gocognit // Response evidence and two cursor requests form one list contract.
func TestListAdaptersMapsHealthAndPaginates(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	sourceObservedAt := observedAt.Add(-time.Second)
	active := devices.AdapterInstance{ID: apiAdapterID, Health: devices.AdapterHealth{
		Status: devices.AdapterHealthUnhealthy, Source: "adapter",
		Since: observedAt, EvidenceAt: observedAt, SourceObservedAt: &sourceObservedAt,
		Reason: &devices.HealthReason{Code: "hearth.network_unreachable"},
		Runtime: &devices.RuntimeEvidence{
			ID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab", Status: "online",
			SoftwareName: "hearth-adapter-simulator", SoftwareVersion: "0.1.0",
			ClaimedAt: observedAt.Add(-time.Hour), LeaseExpiresAt: observedAt.Add(15 * time.Second),
		},
	}}
	secondAdapter := active
	secondAdapter.ID = "z_adapter"
	calls := 0
	stub := &stubDevices{listAdapters: func(
		_ context.Context,
		params devices.ListAdaptersParams,
	) (devices.Page[devices.AdapterInstance], error) {
		calls++
		if params.Limit != 50 {
			t.Fatalf("params = %#v", params)
		}
		if calls == 1 {
			if params.AfterID != nil {
				t.Fatalf("first AfterID = %v", params.AfterID)
			}
			return devices.Page[devices.AdapterInstance]{Items: []devices.AdapterInstance{active}, HasMore: true}, nil
		}
		if params.AfterID == nil || *params.AfterID != apiAdapterID {
			t.Fatalf("second AfterID = %v", params.AfterID)
		}
		return devices.Page[devices.AdapterInstance]{Items: []devices.AdapterInstance{secondAdapter}}, nil
	}}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/adapters")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var first AdapterCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	body := first.Items[0]
	if body.ID != apiAdapterID || body.Health.Status != "unhealthy" || body.Health.Source != "adapter" ||
		body.Health.SourceObservedAt == nil || *body.Health.SourceObservedAt != formatTime(sourceObservedAt) ||
		body.Health.Reason == nil || body.Health.Reason.Code != "hearth.network_unreachable" ||
		body.Health.Runtime == nil || body.Health.Runtime.LastHeartbeatAt != nil {
		t.Fatalf("Adapter body = %#v", body)
	}
	if !strings.Contains(response.Body.String(), `"last_heartbeat_at":null`) {
		t.Fatalf("claimed runtime did not expose null last_heartbeat_at: %s", response.Body.String())
	}

	response = performRequest(router, "/v1/adapters?cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second AdapterCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "z_adapter" || second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
	operation := openapi.OpenAPI().Paths["/v1/adapters"].Get
	if operation == nil || operation.OperationID != "list-adapters" || len(operation.Tags) != 1 ||
		operation.Tags[0] != "Adapters" {
		t.Fatalf("list Adapters operation = %#v", operation)
	}
}

func TestGetAdapterMapsCurrentHealth(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	stub := &stubDevices{getAdapter: func(_ context.Context, adapterID string) (devices.AdapterInstance, error) {
		if adapterID != apiAdapterID {
			t.Fatalf("Adapter ID = %q", adapterID)
		}
		return devices.AdapterInstance{ID: adapterID, Health: devices.AdapterHealth{
			Status: devices.AdapterHealthUnknown, Source: "core", Since: observedAt, EvidenceAt: observedAt,
			Reason: &devices.HealthReason{Code: "hearth.awaiting_health"},
		}}, nil
	}}
	router, _ := testAPI(t, stub)
	response := performRequest(router, "/v1/adapters/"+apiAdapterID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body AdapterBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != apiAdapterID || body.Health.Status != "unknown" || body.Health.Source != "core" ||
		body.Health.SourceObservedAt != nil || body.Health.Reason == nil ||
		body.Health.Reason.Code != "hearth.awaiting_health" {
		t.Fatalf("Adapter body = %#v", body)
	}
}

func TestAdapterAndHistoryReadsMapErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		path   string
		stub   *stubDevices
		status int
		detail string
	}{
		{
			"list internal", "/v1/adapters",
			&stubDevices{listAdapters: func(context.Context, devices.ListAdaptersParams) (
				devices.Page[devices.AdapterInstance],
				error,
			) {
				return devices.Page[devices.AdapterInstance]{}, errors.New("SQLite unavailable")
			}},
			http.StatusInternalServerError, "internal error",
		},
		{"detail invalid ID", "/v1/adapters/Bad.Adapter", &stubDevices{}, http.StatusBadRequest,
			"adapter_id must be a subject-safe slug"},
		{
			"detail not found", "/v1/adapters/" + apiAdapterID,
			&stubDevices{getAdapter: func(context.Context, string) (devices.AdapterInstance, error) {
				return devices.AdapterInstance{}, devices.ErrAdapterNotFound
			}},
			http.StatusNotFound, "adapter not found",
		},
		{
			"Adapter history not found", "/v1/adapters/" + apiAdapterID + "/health/history",
			&stubDevices{listAdapterHealthHistory: func(context.Context, devices.ListAdapterHealthParams) (
				devices.Page[devices.HealthTransition],
				error,
			) {
				return devices.Page[devices.HealthTransition]{}, devices.ErrAdapterNotFound
			}},
			http.StatusNotFound, "adapter not found",
		},
		{"Entity history invalid ID", "/v1/entities/bad/availability/history", &stubDevices{},
			http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID"},
		{
			"Entity history not found", "/v1/entities/" + string(apiEntityID) + "/availability/history",
			&stubDevices{listEntityAvailabilityHistory: func(context.Context, devices.ListEntityAvailabilityParams) (
				devices.Page[devices.HealthTransition],
				error,
			) {
				return devices.Page[devices.HealthTransition]{}, devices.ErrEntityNotFound
			}},
			http.StatusNotFound, "entity not found",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, test.stub)
			response := performRequest(router, test.path)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var problem huma.ErrorModel
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Detail != test.detail {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

//nolint:gocognit // Both history resources share the transition body and cursor contract.
func TestHealthHistoryRoutesMapEvidenceAndScopedCursors(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	sourceObservedAt := observedAt.Add(-time.Second)
	adapterCalls := 0
	stub := &stubDevices{
		listAdapterHealthHistory: func(
			_ context.Context,
			params devices.ListAdapterHealthParams,
		) (devices.Page[devices.HealthTransition], error) {
			adapterCalls++
			if params.AdapterID != apiAdapterID || params.Limit != 50 {
				t.Fatalf("Adapter history params = %#v", params)
			}
			if adapterCalls == 1 {
				if params.BeforeReceiveOrder != nil {
					t.Fatalf("first Adapter position = %v", params.BeforeReceiveOrder)
				}
				return devices.Page[devices.HealthTransition]{
					Items: []devices.HealthTransition{{
						ReceiveOrder: 12, Status: "unhealthy", Source: "adapter",
						Reason:           &devices.HealthReason{Code: "hearth.network_unreachable"},
						SourceObservedAt: &sourceObservedAt, ObservedAt: observedAt,
					}},
					HasMore: true,
				}, nil
			}
			if params.BeforeReceiveOrder == nil || *params.BeforeReceiveOrder != 12 {
				t.Fatalf("second Adapter position = %v", params.BeforeReceiveOrder)
			}
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{{
				ReceiveOrder: 4, Status: "unknown", Source: "core",
				Reason: &devices.HealthReason{Code: "hearth.awaiting_health"}, ObservedAt: observedAt.Add(-time.Hour),
			}}}, nil
		},
		listEntityAvailabilityHistory: func(
			_ context.Context,
			params devices.ListEntityAvailabilityParams,
		) (devices.Page[devices.HealthTransition], error) {
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

	response := performRequest(router, "/v1/adapters/"+apiAdapterID+"/health/history")
	if response.Code != http.StatusOK {
		t.Fatalf("Adapter history status = %d, body = %s", response.Code, response.Body.String())
	}
	var first HealthTransitionCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == nil || first.Items[0].Reason == nil ||
		first.Items[0].Reason.Code != "hearth.network_unreachable" || first.Items[0].SourceObservedAt == nil ||
		first.Items[0].ObservedAt != formatTime(observedAt) {
		t.Fatalf("Adapter history = %#v", first)
	}
	response = performRequest(router, "/v1/adapters/"+apiAdapterID+"/health/history?cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second Adapter history status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(router, "/v1/entities/"+string(apiEntityID)+"/availability/history")
	if response.Code != http.StatusOK {
		t.Fatalf("Entity history status = %d, body = %s", response.Code, response.Body.String())
	}
	var entityHistory HealthTransitionCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &entityHistory); err != nil {
		t.Fatal(err)
	}
	if len(entityHistory.Items) != 1 || entityHistory.Items[0].Status != "unavailable" ||
		entityHistory.Items[0].Source != "adapter_health" || entityHistory.Items[0].SourceObservedAt != nil {
		t.Fatalf("Entity history = %#v", entityHistory)
	}
	wrongRoute := performRequest(
		router,
		"/v1/entities/"+string(apiEntityID)+"/availability/history?cursor="+*first.NextCursor,
	)
	if wrongRoute.Code != http.StatusBadRequest {
		t.Fatalf("cross-route cursor status = %d, body = %s", wrongRoute.Code, wrongRoute.Body.String())
	}
}
