package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	apiEntityID      = devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiDeviceID      = devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiObservationID = devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-0123456789ab")
)

type stubRepository struct {
	view devices.EntityView
	err  error
}

func (*stubRepository) RegisterBinding(context.Context, devices.RegisterBindingParams) (devices.Binding, error) {
	panic("unexpected RegisterBinding call")
}

func (repository *stubRepository) GetEntityView(context.Context, devices.EntityID) (devices.EntityView, error) {
	return repository.view, repository.err
}

func (*stubRepository) ProjectObservation(context.Context, devices.ProjectObservationParams) (devices.ProjectionResult, error) {
	panic("unexpected ProjectObservation call")
}

func (*stubRepository) DeleteExpiredObservationReceipts(context.Context, time.Time) error {
	panic("unexpected DeleteExpiredObservationReceipts call")
}

func TestGetEntityReturnsMetadataAndNullableState(t *testing.T) {
	repository := &stubRepository{view: apiEntityView(nil)}
	router, openapi := testAPI(t, repository)

	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ID      string         `json:"id"`
		Name    string         `json:"name"`
		Support map[string]any `json:"support"`
		State   any            `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != string(apiEntityID) || body.Name != "Power" || body.State != nil || body.Support["state"] == nil {
		t.Fatalf("body = %#v", body)
	}
	operation := openapi.OpenAPI().Paths["/v1/entities/{entity_id}"].Get
	if operation == nil || operation.OperationID != "get-entity" {
		t.Fatalf("GET operation = %#v", operation)
	}
}

func TestGetEntityMapsCurrentState(t *testing.T) {
	adapterReceivedAt := time.Date(2026, 8, 22, 12, 0, 0, 123, time.UTC)
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	observedAt := adapterReceivedAt.Add(time.Second)
	state := &devices.State{
		EntityID: apiEntityID, Value: devices.Value(`true`), ObservationID: apiObservationID,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: &sourceUpdatedAt, ObservedAt: observedAt, ReceiveOrder: 4,
	}
	router, _ := testAPI(t, &stubRepository{view: apiEntityView(state)})
	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body EntityBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State == nil || body.State.Value != true || body.State.ObservationID != string(apiObservationID) ||
		body.State.SourceUpdatedAt == nil || *body.State.SourceUpdatedAt != sourceUpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetEntityMapsStableErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		repository *stubRepository
		status     int
		code       string
	}{
		{"invalid ID", "/v1/entities/not-an-id", &stubRepository{}, http.StatusBadRequest, "invalid_request"},
		{"not found", "/v1/entities/" + string(apiEntityID), &stubRepository{err: devices.ErrEntityNotFound}, http.StatusNotFound, "entity_not_found"},
		{"internal", "/v1/entities/" + string(apiEntityID), &stubRepository{err: errors.New("SQLite unavailable")}, http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, _ := testAPI(t, test.repository)
			response := performRequest(router, test.path)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body ErrorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != test.code {
				t.Fatalf("error body = %#v", body)
			}
		})
	}
}

func apiEntityView(state *devices.State) devices.EntityView {
	return devices.EntityView{
		Entity: devices.Entity{
			ID: apiEntityID, DeviceID: apiDeviceID, AdapterID: "simulator", Name: "Power",
			TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		},
		State: state,
	}
}

func testAPI(t *testing.T, repository *stubRepository) (*echo.Echo, huma.API) {
	t.Helper()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(repository, catalog, devices.Dependencies{})
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	group := huma.NewGroup(openapi, "/v1/entities")
	Register(group, service)
	return router, openapi
}

func performRequest(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
