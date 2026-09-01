package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const apiAdapterID = "simulator"

//nolint:gocognit,gocyclo,cyclop // Response evidence and two cursor requests form one list contract.
func TestListAdaptersMapsHealthAndScopesCursorToArchiveFilter(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 8, 29, 15, 0, 1, 0, time.UTC)
	sourceObservedAt := observedAt.Add(-time.Second)
	active := devices.AdapterInstance{ID: apiAdapterID, Health: &devices.AdapterHealth{
		Status: devices.AdapterHealthUnhealthy, Since: observedAt, EvidenceAt: observedAt,
		Reason: &devices.HealthReason{Code: "hearth.network_unreachable"},
		Runtime: &devices.RuntimeEvidence{
			ID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab", Status: "online",
			SoftwareName: "hearth-adapter-simulator", SoftwareVersion: "0.1.0",
			ClaimedAt: observedAt.Add(-time.Hour), LeaseExpiresAt: observedAt.Add(15 * time.Second),
		},
		ExternalSystem: &devices.ExternalSystemEvidence{
			Status: devices.AdapterHealthUnhealthy, SourceObservedAt: sourceObservedAt,
			EvidenceAt: observedAt,
			Reason:     &devices.HealthReason{Code: "hearth.network_unreachable"},
		},
	}}
	archivedAt := observedAt.Add(time.Hour)
	archived := devices.AdapterInstance{ID: "z_archive", ArchivedAt: &archivedAt}
	calls := 0
	stub := &stubDevices{listAdapters: func(
		_ context.Context,
		params devices.ListAdaptersParams,
	) (devices.Page[devices.AdapterInstance], error) {
		calls++
		if params.Limit != 50 || !params.IncludeArchived {
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
		return devices.Page[devices.AdapterInstance]{Items: []devices.AdapterInstance{archived}}, nil
	}}
	router, openapi := testAPI(t, stub)

	response := performRequest(router, "/v1/adapters?include_archived=true")
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
	if body.ID != apiAdapterID || body.Health == nil || body.Health.Status != "unhealthy" ||
		body.Health.Reason == nil || body.Health.Reason.Code != "hearth.network_unreachable" ||
		body.Health.Runtime == nil || body.Health.Runtime.LastHeartbeatAt != nil ||
		body.Health.ExternalSystem == nil || body.Health.ExternalSystem.SourceObservedAt != formatTime(sourceObservedAt) {
		t.Fatalf("Adapter body = %#v", body)
	}
	if !strings.Contains(response.Body.String(), `"last_heartbeat_at":null`) {
		t.Fatalf("claimed runtime did not expose null last_heartbeat_at: %s", response.Body.String())
	}

	mismatched := performRequest(router, "/v1/adapters?cursor="+*first.NextCursor)
	if mismatched.Code != http.StatusBadRequest || calls != 1 {
		t.Fatalf("mismatched filter status/calls = %d/%d, body = %s", mismatched.Code, calls, mismatched.Body.String())
	}
	response = performRequest(router, "/v1/adapters?include_archived=true&cursor="+*first.NextCursor)
	if response.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", response.Code, response.Body.String())
	}
	var second AdapterCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ArchivedAt == nil || second.Items[0].Health != nil ||
		second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
	if !strings.Contains(response.Body.String(), `"health":null`) {
		t.Fatalf("archived Adapter did not expose null health: %s", response.Body.String())
	}
	operation := openapi.OpenAPI().Paths["/v1/adapters"].Get
	if operation == nil || operation.OperationID != "list-adapters" || len(operation.Tags) != 1 ||
		operation.Tags[0] != "Adapters" {
		t.Fatalf("list Adapters operation = %#v", operation)
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

//nolint:gocognit // Success and conflict cases share one archive route contract.
func TestGetAndArchiveAdapterMapErrorsAndNoContent(t *testing.T) {
	t.Parallel()
	archivedAt := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	stub := &stubDevices{
		getAdapter: func(_ context.Context, adapterID string) (devices.AdapterInstance, error) {
			if adapterID != apiAdapterID {
				t.Fatalf("Adapter ID = %q", adapterID)
			}
			return devices.AdapterInstance{ID: adapterID, ArchivedAt: &archivedAt}, nil
		},
		archiveAdapter: func(_ context.Context, adapterID string) error {
			if adapterID != apiAdapterID {
				t.Fatalf("Adapter ID = %q", adapterID)
			}
			return nil
		},
	}
	router, openapi := testAPI(t, stub)
	response := performRequest(router, "/v1/adapters/"+apiAdapterID)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"health":null`) {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}
	response = performMethodRequest(router, http.MethodDelete, "/v1/adapters/"+apiAdapterID)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("archive status = %d, body = %s", response.Code, response.Body.String())
	}
	operation := openapi.OpenAPI().Paths["/v1/adapters/{adapter_id}"].Delete
	if operation == nil || operation.OperationID != "archive-adapter" {
		t.Fatalf("archive operation = %#v", operation)
	}

	for _, test := range []struct {
		name   string
		id     string
		err    error
		status int
		detail string
	}{
		{"invalid ID", "Bad.Adapter", nil, http.StatusBadRequest, "adapter_id must be a subject-safe slug"},
		{"not found", apiAdapterID, devices.ErrAdapterNotFound, http.StatusNotFound, "adapter not found"},
		{"active", apiAdapterID, devices.ErrAdapterActive, http.StatusConflict, "adapter has an active runtime"},
		{"owned bindings", apiAdapterID, devices.ErrAdapterHasBindings, http.StatusConflict, "adapter owns bindings"},
		{"internal", apiAdapterID, errors.New("SQLite unavailable"), http.StatusInternalServerError, "internal error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failureStub := &stubDevices{}
			if test.err != nil {
				failureStub.archiveAdapter = func(context.Context, string) error { return test.err }
			}
			failureRouter, _ := testAPI(t, failureStub)
			failure := performMethodRequest(failureRouter, http.MethodDelete, "/v1/adapters/"+test.id)
			if failure.Code != test.status {
				t.Fatalf("status = %d, body = %s", failure.Code, failure.Body.String())
			}
			var problem huma.ErrorModel
			if err := json.Unmarshal(failure.Body.Bytes(), &problem); err != nil {
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
						ReceiveOrder: 12, Status: "unhealthy", Source: "external_system",
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

func performMethodRequest(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
