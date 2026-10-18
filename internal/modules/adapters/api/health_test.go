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

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const apiAdapterID = "simulator"

//nolint:gocognit // Response evidence and two cursor requests form one list contract.
func TestListAdaptersMapsHealthAndPaginates(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	sourceObservedAt := observedAt.Add(-time.Second)
	active := adapters.AdapterInstance{ID: apiAdapterID, Health: adapters.AdapterHealth{
		Status: adapters.AdapterHealthUnhealthy, Source: "adapter",
		Since: observedAt, EvidenceAt: observedAt, SourceObservedAt: &sourceObservedAt,
		Reason: &adapters.HealthReason{Code: "hearth.network_unreachable"},
		Runtime: &adapters.RuntimeEvidence{
			ID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab", Status: "online",
			SoftwareName: "hearth-adapter-simulator", SoftwareVersion: "0.1.0",
			ClaimedAt: observedAt.Add(-time.Hour), LeaseExpiresAt: observedAt.Add(15 * time.Second),
		},
	}}
	secondAdapter := active
	secondAdapter.ID = "z_adapter"
	calls := 0
	stub := &stubAdapters{listAdapters: func(
		_ context.Context,
		params adapters.ListAdaptersParams,
	) (adapters.Page[adapters.AdapterInstance], error) {
		calls++
		if params.Limit != 50 {
			t.Fatalf("params = %#v", params)
		}
		if calls == 1 {
			if params.AfterID != nil {
				t.Fatalf("first AfterID = %v", params.AfterID)
			}
			return adapters.Page[adapters.AdapterInstance]{
				Items:   []adapters.AdapterInstance{active},
				HasMore: true,
			}, nil
		}
		if params.AfterID == nil || *params.AfterID != apiAdapterID {
			t.Fatalf("second AfterID = %v", params.AfterID)
		}
		return adapters.Page[adapters.AdapterInstance]{Items: []adapters.AdapterInstance{secondAdapter}}, nil
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
	stub := &stubAdapters{getAdapter: func(_ context.Context, adapterID string) (adapters.AdapterInstance, error) {
		if adapterID != apiAdapterID {
			t.Fatalf("Adapter ID = %q", adapterID)
		}
		return adapters.AdapterInstance{ID: adapterID, Health: adapters.AdapterHealth{
			Status: adapters.AdapterHealthUnknown, Source: "core", Since: observedAt, EvidenceAt: observedAt,
			Reason: &adapters.HealthReason{Code: "hearth.awaiting_health"},
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
		stub   *stubAdapters
		status int
		detail string
	}{
		{
			"list internal", "/v1/adapters",
			&stubAdapters{listAdapters: func(context.Context, adapters.ListAdaptersParams) (
				adapters.Page[adapters.AdapterInstance],
				error,
			) {
				return adapters.Page[adapters.AdapterInstance]{}, errors.New("SQLite unavailable")
			}},
			http.StatusInternalServerError, "internal error",
		},
		{"detail invalid ID", "/v1/adapters/Bad.Adapter", &stubAdapters{}, http.StatusBadRequest,
			"adapter_id must be a subject-safe slug"},
		{
			"detail not found", "/v1/adapters/" + apiAdapterID,
			&stubAdapters{getAdapter: func(context.Context, string) (adapters.AdapterInstance, error) {
				return adapters.AdapterInstance{}, adapters.ErrAdapterNotFound
			}},
			http.StatusNotFound, "adapter not found",
		},
		{
			"Adapter history not found", "/v1/adapters/" + apiAdapterID + "/health/history",
			&stubAdapters{listAdapterHealthHistory: func(context.Context, adapters.ListAdapterHealthParams) (
				adapters.Page[adapters.HealthTransition],
				error,
			) {
				return adapters.Page[adapters.HealthTransition]{}, adapters.ErrAdapterNotFound
			}},
			http.StatusNotFound, "adapter not found",
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

//nolint:gocognit // Evidence mapping and two cursor requests form one history contract.
func TestHealthHistoryRoutesMapEvidenceAndScopedCursors(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	sourceObservedAt := observedAt.Add(-time.Second)
	adapterCalls := 0
	stub := &stubAdapters{
		listAdapterHealthHistory: func(
			_ context.Context,
			params adapters.ListAdapterHealthParams,
		) (adapters.Page[adapters.HealthTransition], error) {
			adapterCalls++
			if params.AdapterID != apiAdapterID || params.Limit != 50 {
				t.Fatalf("Adapter history params = %#v", params)
			}
			if adapterCalls == 1 {
				if params.BeforeReceiveOrder != nil {
					t.Fatalf("first Adapter position = %v", params.BeforeReceiveOrder)
				}
				return adapters.Page[adapters.HealthTransition]{
					Items: []adapters.HealthTransition{{
						ReceiveOrder: 12, Status: "unhealthy", Source: "adapter",
						Reason:           &adapters.HealthReason{Code: "hearth.network_unreachable"},
						SourceObservedAt: &sourceObservedAt, ObservedAt: observedAt,
					}},
					HasMore: true,
				}, nil
			}
			if params.BeforeReceiveOrder == nil || *params.BeforeReceiveOrder != 12 {
				t.Fatalf("second Adapter position = %v", params.BeforeReceiveOrder)
			}
			return adapters.Page[adapters.HealthTransition]{Items: []adapters.HealthTransition{{
				ReceiveOrder: 4, Status: "unknown", Source: "core",
				Reason: &adapters.HealthReason{Code: "hearth.awaiting_health"}, ObservedAt: observedAt.Add(-time.Hour),
			}}}, nil
		},
	}
	router, _ := testAPI(t, stub)

	response := performRequest(router, "/v1/adapters/"+apiAdapterID+"/health/history")
	if response.Code != http.StatusOK {
		t.Fatalf("Adapter history status = %d, body = %s", response.Code, response.Body.String())
	}
	var first AdapterHealthTransitionCollectionBody
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

	wrongRoute := performRequest(
		router,
		"/v1/adapters/other/health/history?cursor="+*first.NextCursor,
	)
	if wrongRoute.Code != http.StatusBadRequest {
		t.Fatalf("cross-route cursor status = %d, body = %s", wrongRoute.Code, wrongRoute.Body.String())
	}
}
